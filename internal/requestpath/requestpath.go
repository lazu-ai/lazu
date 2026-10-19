// Package requestpath is the data boundary of gateway authentication and
// financial admission. Request handlers use this interface instead of SQL
// handles. Query inventory (changes require updating this comment):
//
//   - ordinary credential, warm identity: no SQL;
//   - cold identity: one token/project/member/actor/payer JOIN and one budget
//     definition query, protected by shared generation fences;
//   - ephemeral credentials: authoritative credential/token queries;
//   - trusted internal/session credentials: authoritative token, principal and
//     user queries, because these uncommon mechanisms retain server/session
//     ownership validation;
//   - Redis Reserve/Settle/Release/Refund/Extend: no SQL on their warm path;
//     cold/recovery admission may read a consistent financial snapshot, and an
//     unknown settlement consults permanent financial identity;
//   - SQLite development only: the existing synchronous token/user/credential/
//     principal checks and transactional preconsume/settlement.
package requestpath

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
)

type Gateway interface {
	Identity(context.Context, string) (*model.IdentitySnapshot, error)
	VerifiedIdentity(context.Context, int, uint) (*model.IdentitySnapshot, error)
	PlaygroundIdentity(context.Context, int) (*model.IdentitySnapshot, error)
	LegacyToken(string) (*model.Token, error)
	TokenByID(int) (*model.Token, error)
	SessionToken(int, int) (*model.Token, error)
	Credential(int, string, time.Time) (*model.Token, error)
	EphemeralCredential(string) (*model.EphemeralCredential, *model.Token, error)
	EphemeralByID(context.Context, uint) (*model.EphemeralCredential, error)
	Principal(int, time.Time) (model.ProjectPrincipal, error)
	User(int) (*model.UserBase, error)
	Reserve(context.Context, ledger.Input) error
	ReserveAdmitted(context.Context, ledger.Input) (int64, error)
	Settle(context.Context, ledger.Input, int64) error
	SettleWithTelemetry(context.Context, ledger.Input, int64, string) error
	Release(context.Context, ledger.Input) error
	Refund(context.Context, ledger.Input, int64, string) error
	Extend(context.Context, string, time.Time) error
	LegacyPreconsume(model.PreConsumeQuotaWithLedgerInput) error
	LegacySettle(model.AdjustRelayQuotaWithLedgerInput) error
}

type gateway struct{}

func Current() Gateway { return gateway{} }
func (gateway) Identity(ctx context.Context, key string) (*model.IdentitySnapshot, error) {
	return model.ResolveIdentitySnapshot(ctx, key)
}
func (gateway) VerifiedIdentity(ctx context.Context, tokenID int, ephemeralID uint) (*model.IdentitySnapshot, error) {
	return model.ResolveVerifiedIdentitySnapshot(ctx, tokenID, ephemeralID)
}
func (gateway) PlaygroundIdentity(ctx context.Context, userID int) (*model.IdentitySnapshot, error) {
	return model.ResolvePlaygroundIdentitySnapshot(ctx, userID)
}
func (gateway) LegacyToken(key string) (*model.Token, error) { return model.ValidateUserToken(key) }
func (gateway) TokenByID(id int) (*model.Token, error) {
	token, err := model.GetTokenById(id)
	return token, model.IdentityAuthorityQueryError(err)
}
func (gateway) SessionToken(id, userID int) (*model.Token, error) {
	token, err := model.GetSessionTokenByIds(id, userID)
	return token, model.IdentityAuthorityQueryError(err)
}
func (gateway) Credential(id int, key string, now time.Time) (*model.Token, error) {
	return model.ResolveTokenCredential(id, key, now)
}
func (gateway) EphemeralCredential(key string) (*model.EphemeralCredential, *model.Token, error) {
	return model.ValidateEphemeralCredential(key)
}
func (gateway) EphemeralByID(ctx context.Context, id uint) (*model.EphemeralCredential, error) {
	credential := new(model.EphemeralCredential)
	err := model.CoreDB().WithContext(ctx).First(credential, id).Error
	return credential, model.IdentityAuthorityQueryError(err)
}
func (gateway) Principal(id int, now time.Time) (model.ProjectPrincipal, error) {
	return model.ResolveProjectPrincipal(model.CoreDB(), id, now)
}
func (gateway) User(id int) (*model.UserBase, error) { return model.GetUserCache(id) }

func engine() (*ledger.Engine, error) {
	value := model.GatewayLedger()
	if value == nil {
		return nil, ledger.ErrUnavailable
	}
	return value, nil
}
func (gateway) Reserve(ctx context.Context, input ledger.Input) error {
	value, err := engine()
	if err != nil {
		return err
	}
	return value.Reserve(ctx, input)
}
func (gateway) ReserveAdmitted(ctx context.Context, input ledger.Input) (int64, error) {
	value, err := engine()
	if err != nil {
		return 0, err
	}
	return value.ReserveAdmitted(ctx, input)
}
func (gateway) Settle(ctx context.Context, input ledger.Input, actual int64) error {
	value, err := engine()
	if err != nil {
		return err
	}
	return value.Settle(ctx, input, actual)
}
func (gateway) SettleWithTelemetry(ctx context.Context, input ledger.Input, actual int64, envelope string) error {
	value, err := engine()
	if err != nil {
		return err
	}
	return value.SettleWithTelemetry(ctx, input, actual, envelope)
}
func (gateway) Release(ctx context.Context, input ledger.Input) error {
	value, err := engine()
	if err != nil {
		return err
	}
	return value.Release(ctx, input)
}
func (gateway) Refund(ctx context.Context, input ledger.Input, amount int64, id string) error {
	value, err := engine()
	if err != nil {
		return err
	}
	return value.Refund(ctx, input, amount, id)
}
func (gateway) Extend(ctx context.Context, id string, until time.Time) error {
	value, err := engine()
	if err != nil {
		return err
	}
	return value.Extend(ctx, id, until)
}
func (gateway) LegacyPreconsume(input model.PreConsumeQuotaWithLedgerInput) error {
	return model.PreConsumeQuotaWithLedger(input)
}
func (gateway) LegacySettle(input model.AdjustRelayQuotaWithLedgerInput) error {
	return model.AdjustRelayQuotaWithLedger(input)
}
