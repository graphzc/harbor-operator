/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Sample manifests", func() {
	It("are accepted by the CRD schemas", func() {
		files, err := filepath.Glob(filepath.Join("..", "..", "config", "samples", "harbor_*.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(files).NotTo(BeEmpty())
		for _, f := range files {
			data, err := os.ReadFile(f)
			Expect(err).NotTo(HaveOccurred())
			dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
			for {
				var obj unstructured.Unstructured
				if err := dec.Decode(&obj.Object); errors.Is(err, io.EOF) {
					break
				} else {
					Expect(err).NotTo(HaveOccurred(), f)
				}
				if obj.Object == nil {
					continue
				}
				obj.SetNamespace(testNamespace)
				obj.SetName("sample-" + obj.GetName())
				Expect(k8sClient.Create(ctx, &obj, client.DryRunAll)).To(Succeed(), f)
			}
		}
	})
})
