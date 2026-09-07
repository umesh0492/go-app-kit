package outbox

// Storage is an alias for Store for backwards compatibility and architectural nomenclature preferences.
type Storage = Store

// PGStorage is an alias for pgStore.
type PGStorage = pgStore

// NewPGStorage creates a PostgreSQL-backed outbox storage instance with optional dialect configuration.
func NewPGStorage(db DBOperator, opts ...StoreOption) Store {
	return NewPGStore(db, opts...)
}
