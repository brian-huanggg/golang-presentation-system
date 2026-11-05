package upload

import (
	"context"
	"log/slog"
	"time"
)

// Pipeline runs validate -> scan -> store -> commit (Architecture 8.2).
type Pipeline struct {
	Scanner Scanner
	Store   Store
	Log     *slog.Logger
}

// Result describes an accepted file.
type Result struct {
	Key  string
	Kind Kind
	Size int64
}

// compensationTimeout bounds the cleanup delete, which must still run when the
// request context is already cancelled (client went away).
const compensationTimeout = 10 * time.Second

// Accept validates and scans data, stores it under a random key, then calls
// commit (the caller's DB transaction). If commit fails, the stored object is
// deleted so the failure leaves nothing behind (IDEA-006). Nothing is written
// to the store unless validation and the scan both pass.
func (p *Pipeline) Accept(ctx context.Context, filename, declaredType string, data []byte,
	commit func(ctx context.Context, res Result) error) (Result, error) {
	return p.AcceptWith(ctx, filename, declaredType, data, nil, commit)
}

// AcceptWith is Accept with a check between the scan and the store. The scan can take
// seconds, so a caller whose rules depend on time (the deadline, DDL-005) re-checks them
// here instead of writing an object that is certain to be refused. beforeStore may be
// nil; when it returns an error, nothing is stored and that error is returned as is.
func (p *Pipeline) AcceptWith(ctx context.Context, filename, declaredType string, data []byte,
	beforeStore func(ctx context.Context) error,
	commit func(ctx context.Context, res Result) error) (Result, error) {

	kind, err := Validate(filename, declaredType, data)
	if err != nil {
		return Result{}, err
	}
	if err := p.Scanner.Scan(ctx, data); err != nil {
		if IsMalware(err) {
			p.log().Warn("upload rejected: malware", "err", err)
		}
		return Result{}, err
	}
	if beforeStore != nil {
		if err := beforeStore(ctx); err != nil {
			return Result{}, err
		}
	}
	key, err := NewKey()
	if err != nil {
		return Result{}, err
	}
	res := Result{Key: key, Kind: kind, Size: int64(len(data))}

	if err := p.Store.Put(ctx, key, data, mimeFor(kind)); err != nil {
		// A failed or partial PutObject may still have left bytes behind.
		p.compensate(key)
		return Result{}, err
	}
	if err := commit(ctx, res); err != nil {
		p.compensate(key)
		return Result{}, err
	}
	return res, nil
}

func (p *Pipeline) compensate(key string) {
	// WithoutCancel: the request ctx is probably the reason we are here.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), compensationTimeout)
	defer cancel()
	if err := p.Store.Delete(ctx, key); err != nil {
		// Accepted orphan (Architecture 8.2); log so it can be swept later.
		p.log().Error("compensating delete failed; object orphaned", "key", key, "err", err)
	}
}

func (p *Pipeline) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}
