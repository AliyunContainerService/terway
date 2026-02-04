// Package testutil provides shared test helpers. client.go provides thin
// wrappers around controller-runtime client.Client to inject errors for tests.
// controller-runtime's fake client does not support error injection
// (see https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client/fake).
package testutil

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// errStatusWriter implements client.SubResourceWriter and returns a fixed error on Update.
type errStatusWriter struct {
	updateErr error
	real      client.SubResourceWriter
}

func (e *errStatusWriter) Create(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return e.real.Create(ctx, obj, subResource, opts...)
}

func (e *errStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return e.updateErr
}

func (e *errStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return e.real.Patch(ctx, obj, patch, opts...)
}

// ClientWithStatusUpdateErr wraps c and returns statusUpdateErr on Status().Update().
func ClientWithStatusUpdateErr(c client.Client, statusUpdateErr error) client.Client {
	return &clientWithStatusUpdateErr{Client: c, statusUpdateErr: statusUpdateErr}
}

type clientWithStatusUpdateErr struct {
	client.Client
	statusUpdateErr error
}

func (w *clientWithStatusUpdateErr) Status() client.SubResourceWriter {
	return &errStatusWriter{updateErr: w.statusUpdateErr, real: w.Client.Status()}
}

// ClientWithGetErr wraps c and returns getErr on every Get().
func ClientWithGetErr(c client.Client, getErr error) client.Client {
	return &clientWithGetErr{Client: c, getErr: getErr}
}

type clientWithGetErr struct {
	client.Client
	getErr error
}

func (w *clientWithGetErr) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return w.getErr
}

// ClientWithUpdateErr wraps c and returns updateErr on Update().
func ClientWithUpdateErr(c client.Client, updateErr error) client.Client {
	return &clientWithUpdateErr{Client: c, updateErr: updateErr}
}

type clientWithUpdateErr struct {
	client.Client
	updateErr error
}

func (w *clientWithUpdateErr) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	return w.updateErr
}

// GetErrorFunc returns an error for a given key; if non-nil, Get will return that error instead of delegating.
type GetErrorFunc func(key client.ObjectKey) error

// ClientWithGetErrorFunc wraps c and returns error on Get when fn(key) returns non-nil.
func ClientWithGetErrorFunc(c client.Client, fn GetErrorFunc) client.Client {
	return &clientWithGetErrorFunc{Client: c, getError: fn}
}

type clientWithGetErrorFunc struct {
	client.Client
	getError GetErrorFunc
}

func (w *clientWithGetErrorFunc) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if w.getError != nil {
		if err := w.getError(key); err != nil {
			return err
		}
	}
	return w.Client.Get(ctx, key, obj, opts...)
}
