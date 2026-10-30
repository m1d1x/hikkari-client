package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"NekkoVPN/core/engine"
)

func TestRoutingDocumentPreviewDoesNotMutateOrConfigure(t *testing.T) {
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, ProcessRouting: true}}
	c := initAlternate(t, f)
	if err := c.AddRule("bypass", "domains", "mine.example"); err != nil {
		t.Fatal(err)
	}
	before, err := c.RoutingDocument()
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Sources) != 1 || before.Sources[0].Bypass.Domains[0] != "mine.example" {
		t.Fatal(before)
	}
	bytesBefore, err := os.ReadFile(filepath.Join(c.baseDir, "routing.json"))
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	configurationBefore := f.configuration
	f.mu.Unlock()
	raw, err := os.ReadFile("../routing/testdata/document-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := c.PreviewRoutingDocument(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Effective.Bypass.Domains) != 2 || preview.Policy.DefaultTarget != "vpn" {
		t.Fatal(preview)
	}
	if _, err := c.PreviewRoutingDocument(`{"schemaVersion":999}`); err == nil {
		t.Fatal("future version accepted")
	}
	bytesAfter, _ := os.ReadFile(filepath.Join(c.baseDir, "routing.json"))
	if !reflect.DeepEqual(bytesBefore, bytesAfter) {
		t.Fatal("preview modified storage")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !reflect.DeepEqual(configurationBefore, f.configuration) {
		t.Fatal("preview configured backend")
	}
}

func TestRoutingDocumentJSONDispatchAndUnsupportedPreview(t *testing.T) {
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true}}
	c := initAlternate(t, f)
	var result struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(c.Call("RoutingDocument", "")), &result); err != nil || !result.OK {
		t.Fatalf("read dispatch: %v %s", err, result.Data)
	}
	raw, err := os.ReadFile("../routing/testdata/document-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]json.RawMessage{"document": raw})
	if err := json.Unmarshal([]byte(c.Call("PreviewRoutingDocument", string(args))), &result); err != nil || result.OK {
		t.Fatalf("unsupported process preview: %v", err)
	}
}
