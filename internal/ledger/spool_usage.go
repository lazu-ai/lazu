package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

type SpoolUsage struct {
	Files                int64
	Bytes                int64
	FreeBytes            uint64
	QuarantinedFiles     int64
	QuarantinedBytes     int64
	UnresolvedExposure   int64
	UnknownExposureFiles int64
}

// These are admission limits, never financial eviction limits. Already
// accepted settlements are still persisted when the soft disk budget is full.
func (e *Engine) SpoolUsage() (SpoolUsage, error) {
	var usage SpoolUsage
	var err error
	seenExposure := make(map[[32]byte]bool)
	for _, directory := range []string{e.SpoolDir, filepath.Join(e.SpoolDir, "quarantine")} {
		entries, readErr := os.ReadDir(directory)
		quarantined := directory != e.SpoolDir
		if os.IsNotExist(readErr) && quarantined {
			continue
		}
		if readErr != nil {
			return usage, readErr
		}
		for _, entry := range entries {
			payload := filepath.Ext(entry.Name()) == ".json"
			if !payload && !(quarantined && filepath.Ext(entry.Name()) == ".reason") {
				continue
			}
			info, err := entry.Info()
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return usage, err
			}
			if payload {
				usage.Files++
				raw, err := os.ReadFile(filepath.Join(directory, entry.Name()))
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return usage, err
				}
				var event Event
				if err := json.Unmarshal(raw, &event); err != nil || !validSpoolEvent(event) {
					usage.UnknownExposureFiles++
				} else {
					event.Input = normalize(event.Input)
					event.OccurredAt = event.OccurredAt.UTC()
					event.TelemetryEnvelope = ""
					canonical, err := json.Marshal(event)
					if err != nil {
						return usage, err
					}
					digest := sha256.Sum256(canonical)
					if !seenExposure[digest] {
						amount := event.Amount
						if amount < 0 {
							amount = -amount
						}
						if usage.UnresolvedExposure > math.MaxInt64-amount {
							return usage, fmt.Errorf("%w: spool exposure exceeds int64", ErrPermanentOperation)
						}
						usage.UnresolvedExposure += amount
						seenExposure[digest] = true
					}
				}
			}
			usage.Bytes += info.Size()
			if quarantined {
				if payload {
					usage.QuarantinedFiles++
				}
				usage.QuarantinedBytes += info.Size()
			}
		}
	}
	usage.FreeBytes, err = spoolFreeBytes(e.SpoolDir)
	return usage, err
}

// A readable directory on a read-only volume is not a durable fallback.
func (e *Engine) CheckSpoolWritable() error {
	file, err := os.CreateTemp(e.SpoolDir, ".write-check-")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write([]byte("ledger")); err != nil {
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
	if err := syncDirectory(e.SpoolDir); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(e.SpoolDir)
}
