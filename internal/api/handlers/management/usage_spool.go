// Fork addition (usage spool): read and acknowledge the durable usage and
// quota-reading spool. Reads are non-destructive; a consumer acknowledges
// what it has committed and retention drops anything left unread too long.
package management

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagespool"
)

const usageSpoolDefaultLimit = 1000

// GetUsageSpool returns one page of spooled records:
// GET /usage-spool?kind=usage|quota&after=<seq>&limit=<n>.
func (h *Handler) GetUsageSpool(c *gin.Context) {
	spool := usagespool.Default()
	if spool == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage spool disabled"})
		return
	}
	kind := strings.TrimSpace(c.Query("kind"))
	after, errAfter := parseUsageSpoolInt(c.Query("after"), 0)
	if errAfter != nil || after < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "after must be a non-negative integer"})
		return
	}
	limit, errLimit := parseUsageSpoolInt(c.Query("limit"), usageSpoolDefaultLimit)
	if errLimit != nil || limit <= 0 || limit > usagespool.MaxReadLimit {
		c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and " + strconv.Itoa(usagespool.MaxReadLimit)})
		return
	}
	page, errRead := spool.Read(kind, after, int(limit))
	if errors.Is(errRead, usagespool.ErrUnknownKind) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind must be usage or quota"})
		return
	}
	if errRead != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": errRead.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"records":        page.Records,
		"next_after":     page.NextAfter,
		"oldest_seq":     page.OldestSeq,
		"pruned_through": page.PrunedThrough,
	})
}

// AckUsageSpool deletes every record of a kind through a seq:
// POST /usage-spool/ack {"kind": "usage"|"quota", "through": <seq>}.
func (h *Handler) AckUsageSpool(c *gin.Context) {
	spool := usagespool.Default()
	if spool == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage spool disabled"})
		return
	}
	var req struct {
		Kind    string `json:"kind"`
		Through *int64 `json:"through"`
	}
	if errBind := c.ShouldBindJSON(&req); errBind != nil || req.Through == nil || *req.Through < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body must be {\"kind\": \"usage\"|\"quota\", \"through\": <non-negative seq>}"})
		return
	}
	errAck := spool.Ack(strings.TrimSpace(req.Kind), *req.Through)
	if errors.Is(errAck, usagespool.ErrUnknownKind) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind must be usage or quota"})
		return
	}
	if errAck != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": errAck.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func parseUsageSpoolInt(value string, fallback int64) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseInt(value, 10, 64)
}
