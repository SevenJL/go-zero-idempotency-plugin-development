package codec

import (
	"testing"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/port"
)

func TestJSONCodecRoundtrip(t *testing.T) {
	c := JSONCodec{}

	if ct := c.ContentType(); ct != "application/json" {
		t.Errorf("ContentType = %q, want application/json", ct)
	}

	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	original := payload{Name: "test", Count: 42}

	data, err := c.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded payload
	if err := c.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded != original {
		t.Errorf("roundtrip mismatch: %+v != %+v", decoded, original)
	}
}

func TestDefaultCodecRegistryRegisterAndLookup(t *testing.T) {
	r := NewCodecRegistry(nil)

	// Lookup unregistered method returns fallback.
	codec, factory, registered := r.Lookup("/package.Service/Method")
	if registered {
		t.Fatal("unregistered method should not return registered=true")
	}
	if codec == nil {
		t.Fatal("fallback codec should not be nil")
	}
	if factory != nil {
		t.Fatal("factory should be nil for unregistered method")
	}

	// Register a method with a factory.
	type resp struct{ Value string }
	r.Register("/package.Service/Method", codec, func() any { return &resp{} })

	codec2, factory2, registered2 := r.Lookup("/package.Service/Method")
	if !registered2 {
		t.Fatal("registered method should return registered=true")
	}
	if codec2 == nil {
		t.Fatal("registered codec should not be nil")
	}
	if factory2 == nil {
		t.Fatal("factory should not be nil for registered method")
	}

	// Factory creates a new instance each time.
	r1 := factory2()
	r2 := factory2()
	if r1 == r2 {
		t.Fatal("factory should create new instances")
	}
}

func TestDefaultCodecRegistryCustomFallback(t *testing.T) {
	custom := JSONCodec{}
	r := NewCodecRegistry(custom)

	codec, _, _ := r.Lookup("/unknown/Method")
	if codec != custom {
		t.Fatal("custom fallback not returned")
	}
}

func TestDefaultCodecRegistryNilFallback(t *testing.T) {
	r := NewCodecRegistry(nil)

	codec, _, _ := r.Lookup("/unknown/Method")
	if codec == nil {
		t.Fatal("nil fallback should default to JSONCodec")
	}
}

// Compile-time check.
var _ port.RPCCodecRegistry = (*DefaultCodecRegistry)(nil)
