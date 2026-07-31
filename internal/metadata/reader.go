package metadata

import (
	"context"
	"errors"

	"github.com/dennis/myfilesorter/internal/domain"
)

var ErrUnavailable = errors.New("embedded metadata reader is unavailable")

type Reader interface {
	Available() bool
	// Read may be called concurrently by the scanner. Implementations must be
	// safe for concurrent use and should honour cancellation promptly.
	Read(context.Context, string) (domain.EmbeddedMetadata, error)
}

type NoopReader struct{}

func (NoopReader) Available() bool { return false }

func (NoopReader) Read(context.Context, string) (domain.EmbeddedMetadata, error) {
	return domain.EmbeddedMetadata{}, ErrUnavailable
}
