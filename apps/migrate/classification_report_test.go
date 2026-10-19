package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/model"
)

func TestPrechargeClassifierManualReportKeepsStdoutJSON(t *testing.T) {
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outRead.Close()
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer errRead.Close()
	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWrite, errWrite
	defer func() { os.Stdout, os.Stderr = savedOut, savedErr; outWrite.Close(); errWrite.Close() }()
	emitPrechargeClassificationReport(model.LegacyPrechargeClassificationReport{Processed: 1, ManualReview: 1, ManualLedgerIDs: []uint{42}})
	outWrite.Close()
	errWrite.Close()
	output, err := io.ReadAll(outRead)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, err := io.ReadAll(errRead)
	if err != nil {
		t.Fatal(err)
	}
	var report model.LegacyPrechargeClassificationReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("redirected classification report must stay valid JSON: %v", err)
	}
	if report.ManualReview != 1 || len(report.ManualLedgerIDs) != 1 || report.ManualLedgerIDs[0] != 42 {
		t.Fatalf("manual evidence inventory changed: %+v", report)
	}
	if !strings.Contains(string(diagnostic), "Ambiguous rows remain") {
		t.Fatal("operator guidance must go to stderr")
	}
}
