package server

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func extendMap(src, extraMap map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	for k, v := range extraMap {
		dst[k] = v
	}
	return dst
}

type mockEnv map[string]string

func (m mockEnv) getEnv(key string) string {
	return m[key]
}

var _ = Describe("Test configuration validation", func() {
	var me mockEnv
	It("Validates insecure configuration properly", func() {
		By("Catching error for incorrect URL")
		me = mockEnv(map[string]string{
			"ELASTIC_ACCESS_MODE": "insecure",
		})
		getEnv = me.getEnv
		_, err := NewConfigFromEnv()
		Expect(err).Should(HaveOccurred())

		By("Validating insecure access mode correctly.")
		me = extendMap(me, map[string]string{
			"ELASTIC_SCHEME": "http",
			"ELASTIC_HOST":   "127.0.0.1",
			"ELASTIC_PORT":   "9200",
		})
		getEnv = me.getEnv
		cfg, err := NewConfigFromEnv()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(cfg).ShouldNot(BeNil())

		By("Catching the error when username and password are set in insecure access mode.")
		me = extendMap(me, map[string]string{
			"ELASTIC_USERNAME": "bob",
			"ELASTIC_PASSWORD": "cannotsetapassword",
		})
		getEnv = me.getEnv
		_, err = NewConfigFromEnv()
		Expect(err).Should(HaveOccurred())

		By("Catching error for HTTPS backend with no CA.")
		me = mockEnv(map[string]string{
			"ELASTIC_ACCESS_MODE": "insecure",
			"ELASTIC_SCHEME":      "https",
			"ELASTIC_HOST":        "127.0.0.1",
			"ELASTIC_PORT":        "9200",
		})
		getEnv = me.getEnv
		_, err = NewConfigFromEnv()
		Expect(err).Should(HaveOccurred())

		By("Validating HTTPS backend with CA.")
		me = extendMap(me, map[string]string{
			"ELASTIC_CA": "/some/path",
		})
		getEnv = me.getEnv
		cfg, err = NewConfigFromEnv()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(cfg).ShouldNot(BeNil())

		By("Validating HTTPS backend with CA.")
		me = extendMap(me, map[string]string{
			"ELASTIC_CA": "/some/path",
		})
		getEnv = me.getEnv
		cfg, err = NewConfigFromEnv()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(cfg).ShouldNot(BeNil())
	})
	It("Validates serviceuser configuration properly", func() {
		By("Catching error for incorrect URL")
		me = mockEnv(map[string]string{
			"ELASTIC_ACCESS_MODE": "serviceuser",
		})
		getEnv = me.getEnv
		_, err := NewConfigFromEnv()
		Expect(err).Should(HaveOccurred())

		By("Catching error when no credentials are provided.")
		me = extendMap(me, map[string]string{
			"ELASTIC_SCHEME": "http",
			"ELASTIC_HOST":   "127.0.0.1",
			"ELASTIC_PORT":   "9200",
		})
		getEnv = me.getEnv
		_, err = NewConfigFromEnv()
		Expect(err).Should(HaveOccurred())

		By("Validating when credentials are set in serviceuser access mode.")
		me = extendMap(me, map[string]string{
			"ELASTIC_USERNAME": "bob",
			"ELASTIC_PASSWORD": "cannotsetapassword",
		})
		getEnv = me.getEnv
		cfg, err := NewConfigFromEnv()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(cfg).ShouldNot(BeNil())

		By("Catching error for HTTPS backend with no CA.")
		me = extendMap(me, map[string]string{
			"ELASTIC_SCHEME": "https",
		})
		getEnv = me.getEnv
		_, err = NewConfigFromEnv()
		Expect(err).Should(HaveOccurred())

		By("Validating HTTPS backend with CA.")
		me = extendMap(me, map[string]string{
			"ELASTIC_CA": "/some/path",
		})
		getEnv = me.getEnv
		cfg, err = NewConfigFromEnv()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(cfg).ShouldNot(BeNil())

		By("Validating HTTPS backend with CA.")
		me = extendMap(me, map[string]string{
			"ELASTIC_CA": "/some/path",
		})
		getEnv = me.getEnv
		cfg, err = NewConfigFromEnv()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(cfg).ShouldNot(BeNil())
	})
	It("Validates passthrough configuration properly", func() {
		By("Catching error for incorrect URL")
		me = mockEnv(map[string]string{
			"ELASTIC_ACCESS_MODE": "passthrough",
		})
		getEnv = me.getEnv
		_, err := NewConfigFromEnv()
		Expect(err).Should(HaveOccurred())

		By("Validating passthrough access mode correctly.")
		me = extendMap(me, map[string]string{
			"ELASTIC_SCHEME": "http",
			"ELASTIC_HOST":   "127.0.0.1",
			"ELASTIC_PORT":   "9200",
		})
		getEnv = me.getEnv
		cfg, err := NewConfigFromEnv()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(cfg).ShouldNot(BeNil())

		By("Catching the error when username and password are set in passthrough access mode.")
		me = extendMap(me, map[string]string{
			"ELASTIC_USERNAME": "bob",
			"ELASTIC_PASSWORD": "cannotsetapassword",
		})
		getEnv = me.getEnv
		_, err = NewConfigFromEnv()
		Expect(err).Should(HaveOccurred())

		By("Catching error for HTTPS backend with no CA.")
		me = mockEnv(map[string]string{
			"ELASTIC_ACCESS_MODE": "passthrough",
			"ELASTIC_SCHEME":      "https",
			"ELASTIC_HOST":        "127.0.0.1",
			"ELASTIC_PORT":        "9200",
		})
		getEnv = me.getEnv
		_, err = NewConfigFromEnv()
		Expect(err).Should(HaveOccurred())

		By("Validating HTTPS backend with CA.")
		me = extendMap(me, map[string]string{
			"ELASTIC_CA": "/some/path",
		})
		getEnv = me.getEnv
		cfg, err = NewConfigFromEnv()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(cfg).ShouldNot(BeNil())

		By("Validating HTTPS backend with CA.")
		me = extendMap(me, map[string]string{
			"ELASTIC_CA": "/some/path",
		})
		getEnv = me.getEnv
		cfg, err = NewConfigFromEnv()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(cfg).ShouldNot(BeNil())
	})
})
