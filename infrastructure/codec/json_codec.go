package codec

import (
	"encoding/json"

	"github.com/SevenJL/go-zero-idempotency-plugin-development/application/port"
)

// JSONCodec is the default RPCCodec for methods that do not register a
// proto-specific codec.
type JSONCodec struct{}

func (JSONCodec) ContentType() string { return "application/json" }

func (JSONCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (JSONCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

var _ port.RPCCodec = JSONCodec{}
