package valueobject

type Scope struct {
	Service string
	Tenant  string
	User    string
}

func (s Scope) IsZero() bool {
	return s.Service == "" && s.Tenant == "" && s.User == ""
}
