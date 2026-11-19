package registry

import "sort"

// A credential that serves a model under its own name takes precedence over a
// folded entry another credential made for the same name from effort-named
// catalogue ids. While any credential registers the name as itself, the
// folded entries for it are held back: they are not registered, so the name is
// routed, listed and described only by the credentials that serve it under
// that name, while the effort-named ids they were made from stay registered
// and requestable. Once no such credential remains, the held-back entries are
// registered again. The registry re-decides this whenever a credential's
// models change, so the outcome does not depend on registration order.

// holdBackShadowedFoldsLocked records which of a registering client's folded
// entries another client serves under its own name, and returns the models
// that are to be registered now.
func (r *ModelRegistry) holdBackShadowedFoldsLocked(clientID string, models []*ModelInfo) []*ModelInfo {
	r.forgetFoldsLocked(clientID)
	kept := make([]*ModelInfo, 0, len(models))
	var heldBack []*ModelInfo
	for _, model := range models {
		if model != nil && model.EffortFolded && model.ID != "" {
			r.addFoldHolderLocked(model.ID, clientID)
			if r.ownNameServedLocked(model.ID, clientID) {
				heldBack = append(heldBack, model)
				continue
			}
		}
		kept = append(kept, model)
	}
	if len(heldBack) > 0 {
		if r.heldBackFolds == nil {
			r.heldBackFolds = make(map[string][]*ModelInfo)
		}
		r.heldBackFolds[clientID] = heldBack
	}
	return kept
}

// forgetFoldsLocked drops what the registry holds about a client's folded
// entries, held back or registered.
func (r *ModelRegistry) forgetFoldsLocked(clientID string) {
	for _, info := range r.heldBackFolds[clientID] {
		r.removeFoldHolderLocked(info.ID, clientID)
	}
	delete(r.heldBackFolds, clientID)
	for id, info := range r.clientModelInfos[clientID] {
		if info != nil && info.EffortFolded {
			r.removeFoldHolderLocked(id, clientID)
		}
	}
}

func (r *ModelRegistry) addFoldHolderLocked(modelID, clientID string) {
	if r.foldHolders == nil {
		r.foldHolders = make(map[string]map[string]struct{})
	}
	holders := r.foldHolders[modelID]
	if holders == nil {
		holders = make(map[string]struct{})
		r.foldHolders[modelID] = holders
	}
	holders[clientID] = struct{}{}
}

func (r *ModelRegistry) removeFoldHolderLocked(modelID, clientID string) {
	holders := r.foldHolders[modelID]
	delete(holders, clientID)
	if len(holders) == 0 {
		delete(r.foldHolders, modelID)
	}
}

// ownNameServedLocked reports whether a client other than exceptClientID
// registers modelID as a model of its own rather than a folded entry.
func (r *ModelRegistry) ownNameServedLocked(modelID, exceptClientID string) bool {
	for clientID, infos := range r.clientModelInfos {
		if clientID == exceptClientID {
			continue
		}
		if _, registered := r.clientModels[clientID]; !registered {
			continue
		}
		if info := infos[modelID]; info != nil && !info.EffortFolded {
			return true
		}
	}
	return false
}

// ownNamesOverFoldsLocked lists the names a client registers as its own that
// some client also holds a folded entry for.
func (r *ModelRegistry) ownNamesOverFoldsLocked(clientID string) map[string]struct{} {
	infos := r.clientModelInfos[clientID]
	if len(infos) == 0 || len(r.foldHolders) == 0 {
		return nil
	}
	if _, registered := r.clientModels[clientID]; !registered {
		return nil
	}
	names := make(map[string]struct{})
	for modelID := range r.foldHolders {
		if info := infos[modelID]; info != nil && !info.EffortFolded {
			names[modelID] = struct{}{}
		}
	}
	return names
}

// trackOwnNamesLocked notes the names a client serves over folded entries and
// returns a function that, once the client's registration has changed,
// re-decides the folded entries for every name it started or stopped serving.
func (r *ModelRegistry) trackOwnNamesLocked(clientID string) func() {
	before := r.ownNamesOverFoldsLocked(clientID)
	return func() {
		after := r.ownNamesOverFoldsLocked(clientID)
		affected := make(map[string]struct{})
		for modelID := range before {
			if _, still := after[modelID]; !still {
				affected[modelID] = struct{}{}
			}
		}
		for modelID := range after {
			if _, already := before[modelID]; !already {
				affected[modelID] = struct{}{}
			}
		}
		holders := make(map[string]struct{})
		for modelID := range affected {
			for holder := range r.foldHolders[modelID] {
				if holder != clientID {
					holders[holder] = struct{}{}
				}
			}
		}
		ordered := make([]string, 0, len(holders))
		for holder := range holders {
			ordered = append(ordered, holder)
		}
		sort.Strings(ordered)
		for _, holder := range ordered {
			r.redecideFoldsLocked(holder)
		}
	}
}

// redecideFoldsLocked registers a client's folded entries that no other client
// now serves under its own name and holds back those another client now does,
// keeping the state of the client's other models.
func (r *ModelRegistry) redecideFoldsLocked(clientID string) {
	modelIDs, registered := r.clientModels[clientID]
	if !registered {
		return
	}
	infos := r.clientModelInfos[clientID]
	changed := false
	for _, held := range r.heldBackFolds[clientID] {
		if !r.ownNameServedLocked(held.ID, clientID) {
			changed = true
		}
	}
	for modelID, info := range infos {
		if info != nil && info.EffortFolded && r.ownNameServedLocked(modelID, clientID) {
			changed = true
		}
	}
	if !changed {
		return
	}
	models := make([]*ModelInfo, 0, len(modelIDs)+len(r.heldBackFolds[clientID]))
	for _, modelID := range modelIDs {
		if info := infos[modelID]; info != nil {
			models = append(models, info)
		}
	}
	models = append(models, r.heldBackFolds[clientID]...)
	r.registerClientLocked(clientID, r.clientProviders[clientID], models, true)
}
