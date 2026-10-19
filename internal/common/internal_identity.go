package common

import (
	"context"
	"github.com/lazu-ai/lazu/internal/ledger"
)

type internalIdentityKey struct{}
type internalCallStateKey struct{}

type InternalChargeReceipt struct {
	RequestID       string
	UserID          int
	TokenID         int
	ProjectID       int
	ActorUserID     int
	ChannelID       int
	Model           string
	ChargedMicrousd int64
	PricingVersion  int64
	Unlimited       bool
	SettlementUnix  int64
}

type InternalRelayCallState struct {
	Reservation         *ledger.Input
	Preflight           bool
	SettlementError     error
	ReservationMicrousd int64
	Receipt             *InternalChargeReceipt
}

func WithInternalRelayCallState(ctx context.Context, state *InternalRelayCallState) context.Context {
	return context.WithValue(ctx, internalCallStateKey{}, state)
}
func InternalRelayCallStateFromContext(ctx context.Context) *InternalRelayCallState {
	state, _ := ctx.Value(internalCallStateKey{}).(*InternalRelayCallState)
	return state
}

// This value is never decoded from JSON or headers. Only an authenticated
// in-process request may construct a child relay identity.
type InternalRelayIdentity struct {
	TokenID         int
	UserID          int
	ProjectID       int
	ActorUserID     int
	PayerUserID     int
	EphemeralID     uint
	ClientIP        string
	RequestHost     string
	SessionID       string
	SessionSource   string
	AgentID         string
	ParentRequestID string
	ForceLane       string
}

func WithInternalRelayIdentity(ctx context.Context, identity InternalRelayIdentity) context.Context {
	return context.WithValue(ctx, internalIdentityKey{}, identity)
}

func InternalRelayIdentityFromContext(ctx context.Context) (InternalRelayIdentity, bool) {
	identity, ok := ctx.Value(internalIdentityKey{}).(InternalRelayIdentity)
	return identity, ok
}
