package provider

import "context"

type ErrorKind int

const (
	Other ErrorKind = iota
	Auth
	RateLimit
)

type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string                  { return e.Message }
func Fail(kind ErrorKind, message string) error { return &Error{kind, message} }

type Status struct {
	CheckedIn, DidCheckedIn bool
	CreditsToday            *float64
	StreakDays              *float64
	Message                 string
}
type Outcome struct {
	Success, Already bool
	Credits          *float64
	Message          string
}
type Batch struct {
	Remain              float64
	Expire, Limit, Used *float64
	Created             *float64
	Name                string
}
type Snapshot struct {
	Balance               float64
	TotalLimit, TotalUsed *float64
	Batches               []Batch
	Source                string
}
type Provider interface {
	Status(context.Context) (Status, error)
	Claim(context.Context) (Outcome, error)
	FetchPoints(context.Context) (Snapshot, error)
	Close()
}
