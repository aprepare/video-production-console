package portable

import "errors"

var (
	ErrInvalidManifest   = errors.New("invalid payload manifest")
	ErrInvalidOverlay    = errors.New("invalid payload overlay")
	ErrEntryHash         = errors.New("payload entry hash mismatch")
	ErrUnsafePath        = errors.New("unsafe payload path")
	ErrUnexpectedEntry   = errors.New("unexpected payload entry")
	ErrMissingEntry      = errors.New("missing payload entry")
	ErrDuplicateEntry    = errors.New("duplicate payload entry")
	ErrSymlink           = errors.New("payload symlink not allowed")
	ErrInsufficientSpace = errors.New("insufficient disk space")
	ErrOutsideAppRoot    = errors.New("cleanup target outside app root")
	ErrInvalidVersion    = errors.New("invalid version")
	ErrStateWrite        = errors.New("version state write failed")
)
