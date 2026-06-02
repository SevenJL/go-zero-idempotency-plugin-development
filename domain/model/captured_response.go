package model

type CapturedResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
	Codec      string
}

func (r CapturedResponse) Clone() CapturedResponse {
	headers := make(map[string][]string, len(r.Headers))
	for key, values := range r.Headers {
		copied := make([]string, len(values))
		copy(copied, values)
		headers[key] = copied
	}

	body := make([]byte, len(r.Body))
	copy(body, r.Body)

	return CapturedResponse{
		StatusCode: r.StatusCode,
		Headers:    headers,
		Body:       body,
		Codec:      r.Codec,
	}
}

func (r CapturedResponse) IsEmpty() bool {
	return r.StatusCode == 0 && len(r.Headers) == 0 && len(r.Body) == 0 && r.Codec == ""
}
