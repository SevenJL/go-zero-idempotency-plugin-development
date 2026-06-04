package valueobject

// Scope defines the ownership boundary for an idempotency key.
// A key is unique within a (Service, Tenant, User) tuple — this allows
// the same client-provided key to be reused across different services
// or tenants without collision.
//
// Scope is an immutable value object. Use NewScope to construct and the
// getter methods to read individual fields.
type Scope struct {
	service string
	tenant  string
	user    string
}

// NewScope creates a Scope with the given dimensions. All parameters are
// optional — an empty string means "not scoped" on that dimension.
func NewScope(service, tenant, user string) Scope {
	return Scope{service: service, tenant: tenant, user: user}
}

// WithService returns a copy of the Scope with the service field replaced.
// Useful for filling in a default service name from configuration.
func (s Scope) WithService(service string) Scope {
	s.service = service
	return s
}

// Service returns the service dimension of this scope.
func (s Scope) Service() string { return s.service }

// Tenant returns the tenant dimension of this scope.
func (s Scope) Tenant() string { return s.tenant }

// User returns the user dimension of this scope.
func (s Scope) User() string { return s.user }

// IsZero reports whether all three scope dimensions are empty.
func (s Scope) IsZero() bool {
	return s.service == "" && s.tenant == "" && s.user == ""
}
