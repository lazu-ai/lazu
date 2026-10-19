package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrPermanentOperation marks a financial intent that cannot succeed through
// retries. Storage adapters must distinguish this from transport/availability
// failures; an unclassified failure always remains recoverable in order.
var ErrPermanentOperation = errors.New("permanent financial operation failure")

// ErrSpoolConflict rejects changed facts under an already durable identity.
// The first accepted intent remains intact; this is not a disk failure.
var ErrSpoolConflict = fmt.Errorf("%w: conflicting spooled financial identity", ErrPermanentOperation)

type QuarantinedOperation struct {
	File          string    `json:"file"`
	PayloadSHA256 string    `json:"payload_sha256"`
	OperationID   string    `json:"operation_id,omitempty"`
	Kind          string    `json:"kind,omitempty"`
	Amount        int64     `json:"amount,string"`
	AmountKnown   bool      `json:"amount_known"`
	Reason        string    `json:"reason"`
	QuarantinedAt time.Time `json:"quarantined_at"`
}

func permanentReplayError(err error) bool {
	if errors.Is(err, ErrPermanentOperation) {
		return true
	}
	var rejection *Rejection
	return errors.As(err, &rejection) && (rejection.Code == "identity_conflict" || rejection.Code == "terminal" || rejection.Code == "money_range")
}

// The immutable payload and reason are synced before unlinking the active
// copy. A crash can leave both copies, never neither. Quarantines are retained
// until an explicit operator classification; replay must not ACK their money.
func (e *Engine) quarantineSpool(path string, raw []byte, event *Event, cause error) error {
	directory := filepath.Join(e.SpoolDir, "quarantine")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := syncDirectory(e.SpoolDir); err != nil {
		return err
	}
	name := filepath.Base(path)
	target := filepath.Join(directory, name)
	if err := os.Link(path, target); err != nil {
		// API and jobs can share this spool. A competing replayer may finish
		// durable isolation and unlink the source after our ReadFile. ENOENT
		// is safe only when its retained target has the exact same evidence.
		if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		prior, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		if !bytes.Equal(prior, raw) {
			return fmt.Errorf("%w: quarantine evidence conflict for %s", ErrSpoolConflict, name)
		}
	}
	file, err := os.Open(target)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	record := QuarantinedOperation{File: target, PayloadSHA256: hex.EncodeToString(sum[:]), Reason: cause.Error(), QuarantinedAt: e.Now().UTC()}
	if event != nil {
		record.OperationID, record.Kind, record.Amount = event.OperationID, event.Kind, event.Amount
		record.AmountKnown = validSpoolEvent(*event)
	}
	manifest := target + ".reason"
	if prior, err := os.ReadFile(manifest); err == nil {
		if err := json.Unmarshal(prior, &record); err != nil {
			return fmt.Errorf("%w: invalid quarantine manifest: %v", ErrPermanentOperation, err)
		}
		if record.PayloadSHA256 != hex.EncodeToString(sum[:]) {
			return fmt.Errorf("%w: quarantine manifest conflict for %s", ErrSpoolConflict, name)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if err := writeQuarantineManifest(directory, manifest, encoded); err != nil {
			return err
		}
	} else {
		return err
	}
	// Another replayer can create the immutable manifest between the first
	// read and link. Verify the winning metadata before unlinking the source.
	prior, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(prior, &record); err != nil || record.PayloadSHA256 != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("%w: invalid quarantine manifest for %s", ErrSpoolConflict, name)
	}
	record.File = target
	metadata, err := os.Open(manifest)
	if err != nil {
		return err
	}
	metadataSync, metadataClose := metadata.Sync(), metadata.Close()
	if err := errors.Join(metadataSync, metadataClose); err != nil {
		return err
	}
	if err := syncDirectory(directory); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := syncDirectory(e.SpoolDir); err != nil {
		return err
	}
	if e.SpoolQuarantine != nil {
		e.SpoolQuarantine(record)
	}
	return nil
}

func validSpoolEvent(event Event) bool {
	return event.PayerID > 0 && event.RequestID != "" && event.OperationID == event.RequestID &&
		event.ActorID >= 0 && event.ProjectID >= 0 && event.TokenID >= 0 &&
		event.Amount <= safeInteger && event.Amount >= -safeInteger &&
		((event.Kind == "settle" && event.Amount >= 0) || (event.Kind == "release" && event.Amount == 0) || (event.Kind == "refund" && event.Amount < 0))
}

func writeQuarantineManifest(directory, destination string, raw []byte) error {
	file, err := os.CreateTemp(directory, ".reason-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(file.Name(), destination); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}
