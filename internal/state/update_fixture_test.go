package state

import (
	"encoding/json"
	"flag"
	"os"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "перегенерувати фікстури контракту")

func TestMain(m *testing.M) {
	flag.Parse()
	if *update {
		doc, in := sampleDoc(&testing.T{})
		if err := Derive(doc, in); err != nil {
			panic(err)
		}
		b, _ := json.MarshalIndent(doc, "", "  ")
		os.WriteFile("../../contract/fixtures/basic.json", append(b, '\n'), 0o644)

		empty := &Doc{}
		err := Derive(empty, DeriveInput{Now: time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)})
		if err != nil {
			panic(err)
		}
		eb, _ := json.MarshalIndent(empty, "", "  ")
		os.WriteFile("../../contract/fixtures/empty.json", append(eb, '\n'), 0o644)
	}
	os.Exit(m.Run())
}
