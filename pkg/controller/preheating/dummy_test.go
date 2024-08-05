package preheating

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("DummyReconcile", func() {
	Context("DummyReconcile", func() {
		controllerReconciler := &DummyReconcile{}

		_, err := controllerReconciler.Reconcile(context.Background(), reconcile.Request{})
		Expect(err).NotTo(HaveOccurred())
	})
})
