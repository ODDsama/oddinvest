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
		doc, err := Build(sampleInputForUpdate())
		if err != nil {
			panic(err)
		}
		b, _ := json.MarshalIndent(doc, "", "  ")
		os.WriteFile("../../contract/fixtures/basic.json", append(b, '\n'), 0o644)
		empty, err := Build(Input{Now: time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)})
		if err != nil {
			panic(err)
		}
		eb, _ := json.MarshalIndent(empty, "", "  ")
		os.WriteFile("../../contract/fixtures/empty.json", append(eb, '\n'), 0o644)
	}
	os.Exit(m.Run())
}

func sampleInputForUpdate() Input {
	t := &testing.T{}
	_ = t
	return sampleInput(&testing.T{})
}

var _ = time.Now
