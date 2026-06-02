package port

// RPCCodec encodes and decodes gRPC response messages for replay.
// Implementations include JSON (default) and protobuf.
type RPCCodec interface {
	// ContentType returns the MIME type produced by this codec.
	ContentType() string

	// Marshal serialises a value into bytes.
	Marshal(v any) ([]byte, error)

	// Unmarshal deserialises bytes into the provided value (which must be a
	// pointer to the correct response type).
	Unmarshal(data []byte, v any) error
}

// RPCCodecRegistry maps gRPC full-method names to codecs and response
// factories so the interceptor can deserialise cached responses during
// replay.
type RPCCodecRegistry interface {
	// Lookup returns the codec and response factory for the given method.
	// The factory returns a pointer to a zero-valued response message of the
	// correct type. The second return value is false when the method is not
	// registered.
	Lookup(fullMethod string) (RPCCodec, func() any, bool)

	// Register associates a codec and response factory with a method.
	Register(fullMethod string, codec RPCCodec, factory func() any)
}
