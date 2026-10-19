package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/stripe/stripe-go/v81/client"
)

type ProjectPaymentDocumentTask struct{}

func NewProjectPaymentDocumentTask() *ProjectPaymentDocumentTask {
	return &ProjectPaymentDocumentTask{}
}
func (*ProjectPaymentDocumentTask) Name() string            { return "project_payment_documents" }
func (*ProjectPaymentDocumentTask) Interval() time.Duration { return 5 * time.Minute }
func (*ProjectPaymentDocumentTask) Run(ctx context.Context) error {
	return ReconcileProjectPaymentDocuments(ctx, time.Now().UTC())
}

// ReconcileProjectPaymentDocuments repairs missing Stripe document events.
// Each model update still validates the immutable order/customer/intent and
// queues the second email idempotently only when both links are present.
func ReconcileProjectPaymentDocuments(ctx context.Context, now time.Time) (result error) {
	if model.CoreDB() == nil {
		return nil
	}
	release, acquired, err := acquireProjectDocumentLock(ctx)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	released := false
	releaseLock := func() {
		if !released {
			release()
			released = true
		}
	}
	defer releaseLock()
	defer func() {
		result = errors.Join(result, model.QueuePaymentDocumentAlerts(now, email_setting.GetEmailSetting().AdminEmail))
	}()
	since := now.AddDate(0, 0, -30).Unix()
	completed, err := model.ListProjectTopUpsMissingDocumentEmails(50, since)
	if err != nil {
		return err
	}
	returnBase := strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL"))
	if returnBase == "" {
		returnBase = setting.StripeReturnBase
	}
	var failures []error
	for _, order := range completed {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := model.QueueProjectPaymentEmailForTopUp(order.Id, returnBase); err != nil {
			failures = append(failures, fmt.Errorf("backfill payment email for top-up %s: %w", order.PublicID, err))
		}
	}
	orders, err := model.ClaimProjectTopUpsForDocumentReconciliation(50, since, now.Unix())
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	// The selected orders have a durable retry lease. No database connection or
	// advisory lock remains pinned while Stripe performs remote work.
	releaseLock()
	if len(orders) == 0 {
		return errors.Join(failures...)
	}
	if !strings.HasPrefix(setting.StripeApiSecret, "sk_") && !strings.HasPrefix(setting.StripeApiSecret, "rk_") {
		return errors.Join(append(failures, errors.New("Stripe secret is unavailable for payment document reconciliation"))...)
	}
	api := &client.API{}
	api.Init(setting.StripeApiSecret, nil)
	for _, order := range orders {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := reconcileProjectPaymentDocument(ctx, api, order); err != nil {
			failures = append(failures, fmt.Errorf("top-up %s: %w", order.PublicID, err))
		}
	}
	return errors.Join(failures...)
}

func acquireProjectDocumentLock(ctx context.Context) (func(), bool, error) {
	if model.CoreDB().Dialector.Name() != "postgres" {
		return func() {}, true, nil
	}
	sqlDB, err := model.CoreDB().DB()
	if err != nil {
		return nil, false, err
	}
	maxOpen := sqlDB.Stats().MaxOpenConnections
	if maxOpen == 1 {
		sqlDB.SetMaxOpenConns(2)
	}
	restore := func() {
		if maxOpen == 1 {
			sqlDB.SetMaxOpenConns(1)
		}
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		restore()
		return nil, false, err
	}
	const name = "lazu-project-payment-documents"
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtext($1))", name).Scan(&acquired); err != nil {
		_ = conn.Close()
		restore()
		return nil, false, err
	}
	if !acquired {
		_ = conn.Close()
		restore()
		return nil, false, nil
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtext($1))", name)
		_ = conn.Close()
		restore()
	}, true, nil
}

func reconcileProjectPaymentDocument(ctx context.Context, api *client.API, order model.TopUp) error {
	return service.ReconcileProjectPaymentDocument(ctx, api, order)
}
