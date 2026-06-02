package model

type FailureMode string

const (
	FailureModeDelete             FailureMode = "delete"
	FailureModeCache              FailureMode = "cache"
	FailureModeKeepProcessingTTL  FailureMode = "keep_processing_until_ttl"
)
