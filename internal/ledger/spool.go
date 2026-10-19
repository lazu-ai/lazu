package ledger

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PersistSettlement returns only after both file contents and the directory
// entry are durable. The directory must be on the gateway's persistent volume.
// A second submission of the same financial identity never overwrites it.
func (e *Engine) PersistSettlement(input Input, actual int64, kind string) error {
	return e.PersistSettlementWithTelemetry(input, actual, kind, "")
}

// PersistSettlementWithTelemetry keeps the complete request facts alongside
// the financial intent until ReplaySpool can publish both atomically.
func (e *Engine) PersistSettlementWithTelemetry(input Input, actual int64, kind string, envelope string) (failure error) {
	defer func() {
		if failure != nil && !errors.Is(failure, ErrPermanentOperation) && e.SpoolFailure != nil {
			e.SpoolFailure(failure)
		}
	}()
	if e.SpoolDir == "" {
		return errors.New("persistent ledger spool is not configured")
	}
	if kind != "settle" && kind != "release" && kind != "refund" {
		return fmt.Errorf("%w: invalid spooled settlement kind", ErrPermanentOperation)
	}
	if (kind == "settle" && actual < 0) || (kind == "release" && actual != 0) || (kind == "refund" && actual >= 0) {
		return fmt.Errorf("%w: invalid spooled amount", ErrPermanentOperation)
	}
	event := Event{Input: normalize(input), OperationID: input.RequestID, Kind: kind, TelemetryEnvelope: envelope}
	event.Amount = actual
	if !validSpoolEvent(event) {
		return fmt.Errorf("%w: invalid spooled financial identity", ErrPermanentOperation)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("%w: cannot encode spooled financial identity: %v", ErrPermanentOperation, err)
	}
	if err := os.MkdirAll(e.SpoolDir, 0700); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(kind + ":" + input.RequestID))
	name := filepath.Join(e.SpoolDir, hex.EncodeToString(sum[:])+".json")
	if prior, err := os.ReadFile(filepath.Join(e.SpoolDir, "quarantine", filepath.Base(name))); err == nil {
		if sameSpooledFinancialEvent(raw, prior) {
			return fmt.Errorf("%w: financial operation is quarantined", ErrPermanentOperation)
		}
		return ErrSpoolConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if existing, err := os.ReadFile(name); err == nil {
		if sameSpooledFinancialEvent(raw, existing) {
			return syncDirectory(e.SpoolDir)
		}
		return ErrSpoolConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(e.SpoolDir, ".pending-")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Link(temp, name); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, readErr := os.ReadFile(name)
			if readErr == nil && sameSpooledFinancialEvent(raw, existing) {
				return syncDirectory(e.SpoolDir)
			}
			if readErr == nil {
				return ErrSpoolConflict
			}
		}
		return err
	}
	return syncDirectory(e.SpoolDir)
}

// A retry can build newer diagnostic facts while the accepted financial
// operation remains identical. Retain the first durable envelope, and compare
// every financial field after decoding old/new integer and array encodings.
func sameSpooledFinancialEvent(left, right []byte) bool {
	canonical := func(raw []byte) ([]byte, error) {
		var event Event

		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		event.Input = normalize(event.Input)
		event.OccurredAt = event.OccurredAt.UTC()
		event.TelemetryEnvelope = ""
		return json.Marshal(event)
	}
	a, err := canonical(left)
	if err != nil {
		return false
	}
	b, err := canonical(right)
	return err == nil && bytes.Equal(a, b)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (e *Engine) ReplaySpool(ctx context.Context) error {
	if e.SpoolDir == "" {
		return nil
	}
	entries, err := os.ReadDir(e.SpoolDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		e.recordSpoolFailure(err)
		return err
	}
	processed := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if processed >= 500 {
			break
		}
		processed++
		path := filepath.Join(e.SpoolDir, entry.Name())
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			e.recordSpoolFailure(err)
			return err
		}
		var event Event
		// A crash after the durable quarantine link and before the source
		// unlink leaves two copies. Complete isolation rather than retrying a
		// previously classified intent or forgetting its unresolved exposure.
		if _, err := os.Stat(filepath.Join(e.SpoolDir, "quarantine", entry.Name())); err == nil {
			var known *Event
			if json.Unmarshal(raw, &event) == nil {
				known = &event
			}
			if err := e.quarantineSpool(path, raw, known, fmt.Errorf("%w: completing interrupted quarantine", ErrPermanentOperation)); err != nil {
				e.recordSpoolFailure(err)
				return err
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			e.recordSpoolFailure(err)
			return err
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			failure := fmt.Errorf("invalid ledger spool %s: %w", entry.Name(), err)
			if err := e.quarantineSpool(path, raw, nil, failure); err != nil {
				e.recordSpoolFailure(err)
				return err
			}
			continue
		}
		if !validSpoolEvent(event) {
			failure := fmt.Errorf("invalid financial identity in ledger spool %s", entry.Name())
			if err := e.quarantineSpool(path, raw, &event, failure); err != nil {
				e.recordSpoolFailure(err)
				return err
			}
			continue
		}
		if err := e.finishWithTelemetry(ctx, event.Input, event.Amount, event.Kind, event.TelemetryEnvelope); err != nil {
			if !permanentReplayError(err) {
				return err
			}
			if quarantineErr := e.quarantineSpool(path, raw, &event, err); quarantineErr != nil {
				e.recordSpoolFailure(quarantineErr)
				return quarantineErr
			}
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			e.recordSpoolFailure(err)
			return err
		}
		if err := syncDirectory(e.SpoolDir); err != nil {
			e.recordSpoolFailure(err)
			return err
		}
	}
	return nil
}

func (e *Engine) recordSpoolFailure(err error) {
	if !errors.Is(err, ErrPermanentOperation) && e.SpoolFailure != nil {
		e.SpoolFailure(err)
	}
}
