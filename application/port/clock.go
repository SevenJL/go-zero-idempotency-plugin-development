package port

import "time"

type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}
