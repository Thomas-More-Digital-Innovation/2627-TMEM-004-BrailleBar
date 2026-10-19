package platform

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const contractsDir = "../../contracts"

func payloadFor(eventType string) any {
	switch eventType {
	case DocumentUploadedEvent:
		return &DocumentUploaded{}
	case DocumentOcrCompletedEvent:
		return &DocumentOcrCompleted{}
	case DocumentLayoutCompletedEvent:
		return &DocumentLayoutCompleted{}
	case PrintJobCreatedEvent:
		return &PrintJobCreated{}
	case PrintJobStatusEvent:
		return &PrintJobStatus{}
	}
	return nil
}

func compileSchemas(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	f, err := os.Open(filepath.Join(contractsDir, "events.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat() // so a bad occurredAt is rejected
	if err := c.AddResource("events.json", doc); err != nil {
		t.Fatal(err)
	}
	return c
}

// Every example must pass the schema AND decode into the Go struct without unknown fields,
// so a change to the schema that the structs do not follow (or the other way round) fails here.
func TestExamplesFollowContract(t *testing.T) {
	compiler := compileSchemas(t)
	files, err := filepath.Glob(filepath.Join(contractsDir, "examples", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no examples found: %v", err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		t.Run(env.Type, func(t *testing.T) {
			schema, err := compiler.Compile("events.json#/$defs/" + env.Type)
			if err != nil {
				t.Fatal(err)
			}
			inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(inst); err != nil {
				t.Fatalf("example breaks the schema: %v", err)
			}

			payload := payloadFor(env.Type)
			if payload == nil {
				t.Fatalf("no Go struct for event type %s", env.Type)
			}
			dec := json.NewDecoder(bytes.NewReader(env.Payload))
			dec.DisallowUnknownFields()
			if err := dec.Decode(payload); err != nil {
				t.Fatalf("payload does not fit the Go struct: %v", err)
			}
		})
	}
}

func TestSchemaRejectsBrokenEvent(t *testing.T) {
	schema, err := compileSchemas(t).Compile("events.json#/$defs/" + PrintJobStatusEvent)
	if err != nil {
		t.Fatal(err)
	}
	bad := `{"id":"x","type":"print.job.status","occurredAt":"yesterday",
	  "payload":{"jobId":"j","documentId":"d","deviceId":"v","state":"exploded","page":0,"pageCount":0}}`
	inst, _ := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(bad)))
	if schema.Validate(inst) == nil {
		t.Fatal("expected the schema to reject a bad state and timestamp")
	}
}

func TestNewEnvelopeIsValid(t *testing.T) {
	schema, err := compileSchemas(t).Compile("events.json#/$defs/" + DocumentUploadedEvent)
	if err != nil {
		t.Fatal(err)
	}
	env, err := NewEnvelope(DocumentUploadedEvent, DocumentUploaded{DocumentID: "d", DeviceID: "v", Source: "text"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(env)
	inst, _ := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err := schema.Validate(inst); err != nil {
		t.Fatalf("NewEnvelope produced an event the schema rejects: %v", err)
	}
}

func TestNewEnvelopeIDs(t *testing.T) {
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		env, _ := NewEnvelope(PrintJobStatusEvent, PrintJobStatus{})
		if !uuid.MatchString(env.ID) || seen[env.ID] {
			t.Fatalf("bad or repeated id %q", env.ID)
		}
		seen[env.ID] = true
	}
}
