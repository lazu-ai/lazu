package telegram

import "time"

// Functions only this package's tests call: they reset state between tests
// or wait for background work to finish. Kept out of the production build.

// From knowledge.go.
// resetKnowledge clears the cache (tests).
func resetKnowledge() {
	knowledge.Lock()
	knowledge.docs, knowledge.fetchedAt, knowledge.urls, knowledge.fetching = nil, time.Time{}, "", false
	knowledge.Unlock()
}

// From state.go.
// resetLocalState clears process-memory state (tests).
func resetLocalState() {
	local.Lock()
	local.until = map[string]time.Time{}
	local.value = map[string]string{}
	local.Unlock()
}

// From updates.go.
// resetIdentity forgets the cached getMe answer (tests).
func resetIdentity() {
	identity.Lock()
	identity.bot, identity.triedAt, identity.forTokens = botIdentity{}, time.Time{}, ""
	identity.Unlock()
}
