// Fork addition (usage spool): a durable, consumer-acknowledged on-disk
// record of per-request usage and per-account quota readings, kept apart
// from the in-memory usage queue so draining that queue (Home, RESP
// subscribers, the management pop endpoint) cannot consume this copy.
//
// Layout under the spool directory:
//
//	state.json            next seq plus per-kind ack and prune watermarks
//	<kind>/<seq>.jsonl    segments, one JSON object per line, named by the
//	                      first seq they hold
//
// Every line carries a "seq" drawn from one counter shared by all kinds:
// strictly increasing and never reused, across restarts and after
// everything has been acknowledged. Each append is fsynced, so a crash
// loses at most the line being written; a torn final line is truncated
// away on open.
package usagespool

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Record kinds.
const (
	KindUsage = "usage"
	KindQuota = "quota"
)

const (
	stateFileName   = "state.json"
	segmentExt      = ".jsonl"
	segmentMaxBytes = 32 << 20
	pruneInterval   = time.Hour
	// MaxReadLimit caps one Read page.
	MaxReadLimit = 5000
)

// ErrUnknownKind reports a kind other than KindUsage or KindQuota.
var ErrUnknownKind = errors.New("usage spool: unknown kind")

func validKind(kind string) bool { return kind == KindUsage || kind == KindQuota }

type kindState struct {
	// AckedThrough is the highest seq the consumer has acknowledged.
	AckedThrough int64 `json:"acked_through"`
	// PrunedThrough is the highest seq retention dropped before it was
	// acknowledged; zero when retention has never dropped unread records.
	PrunedThrough int64 `json:"pruned_through"`
}

type spoolState struct {
	NextSeq int64                 `json:"next_seq"`
	Kinds   map[string]*kindState `json:"kinds"`
}

type segment struct {
	path      string
	firstSeq  int64
	lastSeq   int64
	size      int64
	openedDay string
	lastWrite time.Time
}

// Spool is one open spool directory. Methods are safe for concurrent use.
type Spool struct {
	dir       string
	retention time.Duration
	now       func() time.Time

	mu       sync.Mutex
	state    spoolState
	segments map[string][]*segment
	active   map[string]*os.File
	closed   bool

	stopPrune chan struct{}
	pruneDone chan struct{}
	closeOnce sync.Once
}

// Open opens (creating if needed) the spool at dir, recovers its seq and
// segment index, prunes records older than retention, and starts the
// hourly retention sweep.
func Open(dir string, retention time.Duration) (*Spool, error) {
	s, err := open(dir, retention, time.Now)
	if err != nil {
		return nil, err
	}
	s.stopPrune = make(chan struct{})
	s.pruneDone = make(chan struct{})
	go s.pruneLoop()
	return s, nil
}

func open(dir string, retention time.Duration, now func() time.Time) (*Spool, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("usage spool: empty directory")
	}
	s := &Spool{
		dir:       dir,
		retention: retention,
		now:       now,
		state:     spoolState{NextSeq: 1, Kinds: map[string]*kindState{}},
		segments:  map[string][]*segment{},
		active:    map[string]*os.File{},
	}
	for _, kind := range []string{KindUsage, KindQuota} {
		if err := os.MkdirAll(filepath.Join(dir, kind), 0o700); err != nil {
			return nil, fmt.Errorf("usage spool: create %s directory: %w", kind, err)
		}
		s.state.Kinds[kind] = &kindState{}
	}
	if err := s.loadState(); err != nil {
		return nil, err
	}
	for _, kind := range []string{KindUsage, KindQuota} {
		if err := s.indexKind(kind); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	err := s.pruneLocked()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Spool) loadState() error {
	data, err := os.ReadFile(filepath.Join(s.dir, stateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("usage spool: read state: %w", err)
	}
	var loaded spoolState
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("usage spool: parse state: %w", err)
	}
	if loaded.NextSeq > s.state.NextSeq {
		s.state.NextSeq = loaded.NextSeq
	}
	for kind, ks := range loaded.Kinds {
		if validKind(kind) && ks != nil {
			s.state.Kinds[kind] = ks
		}
	}
	return nil
}

// saveStateLocked writes state.json atomically (temp file, fsync, rename).
func (s *Spool) saveStateLocked() error {
	data, err := json.Marshal(s.state)
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, stateFileName+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("usage spool: write state: %w", err)
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if errClose := f.Close(); err == nil {
		err = errClose
	}
	if err != nil {
		return fmt.Errorf("usage spool: write state: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, stateFileName)); err != nil {
		return fmt.Errorf("usage spool: write state: %w", err)
	}
	return syncDir(s.dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	errSync := d.Sync()
	if errClose := d.Close(); errSync == nil {
		errSync = errClose
	}
	return errSync
}

// indexKind scans a kind's segments, truncating a torn final line, and
// raises NextSeq past every seq found on disk.
func (s *Spool) indexKind(kind string) error {
	entries, err := os.ReadDir(filepath.Join(s.dir, kind))
	if err != nil {
		return fmt.Errorf("usage spool: list %s: %w", kind, err)
	}
	var segs []*segment
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, segmentExt) {
			continue
		}
		first, errParse := strconv.ParseInt(strings.TrimSuffix(name, segmentExt), 10, 64)
		if errParse != nil {
			continue
		}
		seg, errScan := scanSegment(filepath.Join(s.dir, kind, name), first)
		if errScan != nil {
			return errScan
		}
		if seg == nil {
			continue
		}
		segs = append(segs, seg)
		if seg.lastSeq >= s.state.NextSeq {
			s.state.NextSeq = seg.lastSeq + 1
		}
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].firstSeq < segs[j].firstSeq })
	s.segments[kind] = segs
	return nil
}

// scanSegment indexes one segment file. A segment holding no complete
// record is removed and reported as nil.
func scanSegment(path string, firstSeq int64) (*segment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("usage spool: read segment: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("usage spool: stat segment: %w", err)
	}
	seg := &segment{path: path, firstSeq: firstSeq, lastWrite: info.ModTime(), openedDay: dayOf(info.ModTime())}
	good := 0
	for offset := 0; offset < len(data); {
		end := bytes.IndexByte(data[offset:], '\n')
		if end < 0 {
			break
		}
		line := data[offset : offset+end]
		seq, ok := lineSeq(line)
		if !ok {
			break
		}
		seg.lastSeq = seq
		offset += end + 1
		good = offset
	}
	if good < len(data) {
		if err := os.Truncate(path, int64(good)); err != nil {
			return nil, fmt.Errorf("usage spool: truncate torn segment: %w", err)
		}
	}
	if good == 0 {
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("usage spool: remove empty segment: %w", err)
		}
		return nil, nil
	}
	seg.size = int64(good)
	return seg, nil
}

func lineSeq(line []byte) (int64, bool) {
	var probe struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal(line, &probe); err != nil || probe.Seq <= 0 {
		return 0, false
	}
	return probe.Seq, true
}

func dayOf(t time.Time) string { return t.UTC().Format("2006-01-02") }

// Append assigns the next seq, sets it (and kind) on fields, and appends
// the record as one fsynced line. fields must not already carry "seq" or
// "kind"; any such values are overwritten.
func (s *Spool) Append(kind string, fields map[string]json.RawMessage) (int64, error) {
	if !validKind(kind) {
		return 0, ErrUnknownKind
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errors.New("usage spool: closed")
	}
	seq := s.state.NextSeq
	fields["seq"] = json.RawMessage(strconv.FormatInt(seq, 10))
	fields["kind"] = json.RawMessage(strconv.Quote(kind))
	line, err := json.Marshal(fields)
	if err != nil {
		return 0, fmt.Errorf("usage spool: encode record: %w", err)
	}
	line = append(line, '\n')

	now := s.now()
	f, seg, err := s.activeSegmentLocked(kind, seq, now, int64(len(line)))
	if err != nil {
		return 0, err
	}
	if _, err := f.Write(line); err != nil {
		return 0, fmt.Errorf("usage spool: append: %w", err)
	}
	if err := f.Sync(); err != nil {
		return 0, fmt.Errorf("usage spool: sync: %w", err)
	}
	s.state.NextSeq = seq + 1
	seg.lastSeq = seq
	seg.size += int64(len(line))
	seg.lastWrite = now
	return seq, nil
}

// activeSegmentLocked returns the open segment for kind, rotating to a new
// one when the current segment was opened on an earlier UTC day or would
// exceed segmentMaxBytes.
func (s *Spool) activeSegmentLocked(kind string, seq int64, now time.Time, lineLen int64) (*os.File, *segment, error) {
	segs := s.segments[kind]
	if f := s.active[kind]; f != nil && len(segs) > 0 {
		cur := segs[len(segs)-1]
		if cur.openedDay == dayOf(now) && cur.size+lineLen <= segmentMaxBytes {
			return f, cur, nil
		}
		s.closeActiveLocked(kind)
	} else if len(segs) > 0 {
		// Reopen the newest segment after a restart if it is still current.
		cur := segs[len(segs)-1]
		if cur.openedDay == dayOf(now) && cur.size+lineLen <= segmentMaxBytes {
			f, err := os.OpenFile(cur.path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return nil, nil, fmt.Errorf("usage spool: open segment: %w", err)
			}
			s.active[kind] = f
			return f, cur, nil
		}
	}
	path := filepath.Join(s.dir, kind, fmt.Sprintf("%020d%s", seq, segmentExt))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("usage spool: create segment: %w", err)
	}
	if err := syncDir(filepath.Join(s.dir, kind)); err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("usage spool: sync segment directory: %w", err)
	}
	seg := &segment{path: path, firstSeq: seq, openedDay: dayOf(now), lastWrite: now}
	s.segments[kind] = append(segs, seg)
	s.active[kind] = f
	return f, seg, nil
}

func (s *Spool) closeActiveLocked(kind string) {
	if f := s.active[kind]; f != nil {
		if errClose := f.Close(); errClose != nil {
			log.WithError(errClose).Warnf("usage spool: close %s segment", kind)
		}
		delete(s.active, kind)
	}
}

// Page is one Read result.
type Page struct {
	Records []json.RawMessage
	// NextAfter is the last seq returned, or the effective cursor when the
	// page is empty, so it is always safe to pass back as after.
	NextAfter int64
	// OldestSeq is the oldest retained, unacknowledged seq of the kind.
	OldestSeq *int64
	// PrunedThrough is the highest seq retention dropped unread, if any.
	PrunedThrough *int64
}

// Read returns up to limit unacknowledged records of kind with seq > after,
// oldest first. It does not consume anything.
func (s *Spool) Read(kind string, after int64, limit int) (Page, error) {
	if !validKind(kind) {
		return Page{}, ErrUnknownKind
	}
	if limit <= 0 || limit > MaxReadLimit {
		limit = MaxReadLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.state.Kinds[kind]
	cursor := after
	if ks.AckedThrough > cursor {
		cursor = ks.AckedThrough
	}
	page := Page{Records: []json.RawMessage{}, NextAfter: cursor}
	if ks.PrunedThrough > 0 {
		pruned := ks.PrunedThrough
		page.PrunedThrough = &pruned
	}

	oldestFound := false
	for _, seg := range s.segments[kind] {
		if seg.lastSeq <= ks.AckedThrough {
			continue
		}
		if seg.lastSeq <= cursor && oldestFound {
			continue
		}
		data, err := os.ReadFile(seg.path)
		if err != nil {
			return Page{}, fmt.Errorf("usage spool: read segment: %w", err)
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 0, 64<<10), segmentMaxBytes)
		for scanner.Scan() {
			line := scanner.Bytes()
			seq, ok := lineSeq(line)
			if !ok || seq <= ks.AckedThrough {
				continue
			}
			if !oldestFound {
				oldest := seq
				page.OldestSeq = &oldest
				oldestFound = true
			}
			if seq <= cursor {
				continue
			}
			if len(page.Records) >= limit {
				return page, nil
			}
			page.Records = append(page.Records, json.RawMessage(append([]byte(nil), line...)))
			page.NextAfter = seq
		}
		if err := scanner.Err(); err != nil {
			return Page{}, fmt.Errorf("usage spool: scan segment: %w", err)
		}
	}
	return page, nil
}

// Ack acknowledges every record of kind with seq <= through, deleting
// segments that hold nothing newer. Acknowledgement never moves backwards
// and never passes the last assigned seq.
func (s *Spool) Ack(kind string, through int64) error {
	if !validKind(kind) {
		return ErrUnknownKind
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if through >= s.state.NextSeq {
		through = s.state.NextSeq - 1
	}
	ks := s.state.Kinds[kind]
	if through <= ks.AckedThrough {
		return nil
	}
	ks.AckedThrough = through
	if err := s.saveStateLocked(); err != nil {
		return err
	}
	return s.dropSegmentsLocked(kind, func(seg *segment) bool { return seg.lastSeq <= through })
}

// dropSegmentsLocked removes the kind's segments matching drop.
func (s *Spool) dropSegmentsLocked(kind string, drop func(*segment) bool) error {
	segs := s.segments[kind]
	kept := segs[:0]
	var firstErr error
	for i, seg := range segs {
		if !drop(seg) {
			kept = append(kept, seg)
			continue
		}
		if i == len(segs)-1 {
			s.closeActiveLocked(kind)
		}
		if err := os.Remove(seg.path); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
			firstErr = fmt.Errorf("usage spool: remove segment: %w", err)
		}
	}
	s.segments[kind] = kept
	return firstErr
}

// pruneLocked drops segments whose newest record is older than retention,
// recording the highest unread seq dropped as the kind's PrunedThrough.
func (s *Spool) pruneLocked() error {
	if s.retention <= 0 {
		return nil
	}
	cutoff := s.now().Add(-s.retention)
	changed := false
	var firstErr error
	for _, kind := range []string{KindUsage, KindQuota} {
		ks := s.state.Kinds[kind]
		err := s.dropSegmentsLocked(kind, func(seg *segment) bool {
			if !seg.lastWrite.Before(cutoff) {
				return false
			}
			if seg.lastSeq > ks.AckedThrough && seg.lastSeq > ks.PrunedThrough {
				ks.PrunedThrough = seg.lastSeq
				changed = true
			}
			return true
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if changed {
		if err := s.saveStateLocked(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Prune applies retention now.
func (s *Spool) Prune() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	return s.pruneLocked()
}

func (s *Spool) pruneLoop() {
	defer close(s.pruneDone)
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopPrune:
			return
		case <-ticker.C:
			if errPrune := s.Prune(); errPrune != nil {
				log.WithError(errPrune).Warn("usage spool: retention sweep failed")
			}
		}
	}
}

// Close stops the retention sweep, persists state, and closes segments.
func (s *Spool) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.stopPrune != nil {
			close(s.stopPrune)
			<-s.pruneDone
		}
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for kind := range s.active {
		s.closeActiveLocked(kind)
	}
	return s.saveStateLocked()
}
