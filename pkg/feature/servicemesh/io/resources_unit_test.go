package io_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/servicemesh/io"
	"os"
	"path/filepath"
)

var _ = Describe("Overwriting gateway namespace in ossm env file", func() {
	var (
		tempDir                             string
		namespace                           string
		KfnotebookControllerServiceMeshPath string
	)

	BeforeEach(func() {
		var err error

		tempDir = GinkgoT().TempDir()
		Expect(err).NotTo(HaveOccurred())

		tempFilePath := filepath.Join(tempDir, "ossm.env")
		tempFile, err := os.Create(tempFilePath)
		Expect(err).NotTo(HaveOccurred())

		mockContents := "ISTIO_GATEWAY=defaultnamespace/odh-gateway\nAnotherSetting=value"
		_, err = tempFile.WriteString(mockContents)
		Expect(err).NotTo(HaveOccurred())
		tempFile.Close()

		// Mock needed vars
		KfnotebookControllerServiceMeshPath = tempDir
		namespace = "testnamespace"
	})

	It("should replace gateway name in the file", func() {
		err := io.OverwriteGatewayName(namespace, KfnotebookControllerServiceMeshPath)
		Expect(err).NotTo(HaveOccurred())

		updatedContents, err := os.ReadFile(filepath.Join(KfnotebookControllerServiceMeshPath, "ossm.env"))
		Expect(err).NotTo(HaveOccurred())

		expected := "ISTIO_GATEWAY=testnamespace/odh-gateway"
		Expect(string(updatedContents)).To(ContainSubstring(expected), "Expected content to contain %q, got %q", expected, updatedContents)
	})

	It("should fail if the file does not exist", func() {
		err := io.OverwriteGatewayName(namespace, "wrong_directory")
		Expect(err).To(HaveOccurred())
	})

	It("should not modify other text in the file", func() {
		err := io.OverwriteGatewayName(namespace, KfnotebookControllerServiceMeshPath)
		Expect(err).NotTo(HaveOccurred())

		updatedContents, err := os.ReadFile(filepath.Join(KfnotebookControllerServiceMeshPath, "ossm.env"))
		Expect(err).NotTo(HaveOccurred())

		expected := "AnotherSetting=value"
		Expect(string(updatedContents)).To(ContainSubstring(expected), "Expected content to contain %q, got %q", expected, updatedContents)
	})
})
