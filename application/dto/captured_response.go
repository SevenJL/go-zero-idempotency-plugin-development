package dto

type CapturedResponse struct {
	StatusCode    int
	Headers       map[string][]string
	Body          []byte
	Codec         string
	BodyTruncated bool
}
