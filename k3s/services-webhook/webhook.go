// Copyright © 2022 Antony Chazapis
// Copyright © 2018 Morven Kao
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
    "encoding/json"
    "fmt"
    "io/ioutil"
    "net/http"

    admissionv1 "k8s.io/api/admission/v1"
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
    runtimeScheme = runtime.NewScheme()
    codecs        = serializer.NewCodecFactory(runtimeScheme)
    deserializer  = codecs.UniversalDeserializer()
)

type WebhookServer struct {
    server        *http.Server
}

type patchOperation struct {
    Op    string      `json:"op"`
    Path  string      `json:"path"`
    Value interface{} `json:"value,omitempty"`
}

func updateClusterIP(target *corev1.ServiceSpec) (patch []patchOperation) {
    if target.Type == "ExternalName" {
        return patch
    }
    if target.ClusterIP != "None" {
        patch = append(patch, patchOperation{
            Op:    "replace",
            Path:  "/spec/clusterIP",
            Value: "None",
        })
    } else {
        patch = append(patch, patchOperation{
            Op:   "add",
            Path: "/spec/clusterIP",
            Value: "None",
        })
    }
    return patch
}

// Create mutation patch for resources
func createPatch(service *corev1.Service) ([]byte, error) {
    var patch []patchOperation

    patch = append(patch, updateClusterIP(&service.Spec)...)

    return json.Marshal(patch)
}

// Main mutation process
func (whsvr *WebhookServer) mutate(ar *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
    req := ar.Request
    var service corev1.Service
    if err := json.Unmarshal(req.Object.Raw, &service); err != nil {
        warningLogger.Printf("Could not unmarshal raw object: %v", err)
        return &admissionv1.AdmissionResponse{
            Result: &metav1.Status{
                Message: err.Error(),
            },
        }
    }

    infoLogger.Printf("AdmissionReview for Kind=%v, Namespace=%v Name=%v (%v) UID=%v patchOperation=%v UserInfo=%v",
        req.Kind, req.Namespace, req.Name, service.Name, req.UID, req.Operation, req.UserInfo)

    patchBytes, err := createPatch(&service)
    if err != nil {
        return &admissionv1.AdmissionResponse{
            Result: &metav1.Status{
                Message: err.Error(),
            },
        }
    }

    infoLogger.Printf("AdmissionResponse: patch=%v\n", string(patchBytes))
    return &admissionv1.AdmissionResponse{
        Allowed: true,
        Patch:   patchBytes,
        PatchType: func() *admissionv1.PatchType {
            pt := admissionv1.PatchTypeJSONPatch
            return &pt
        }(),
    }
}

// Serve method for webhook server
func (whsvr *WebhookServer) serve(w http.ResponseWriter, r *http.Request) {
    var body []byte
    if r.Body != nil {
        if data, err := ioutil.ReadAll(r.Body); err == nil {
            body = data
        }
    }
    if len(body) == 0 {
        warningLogger.Println("empty body")
        http.Error(w, "empty body", http.StatusBadRequest)
        return
    }

    // Verify the content type is accurate
    contentType := r.Header.Get("Content-Type")
    if contentType != "application/json" {
        warningLogger.Printf("Content-Type=%s, expect application/json", contentType)
        http.Error(w, "invalid Content-Type, expect `application/json`", http.StatusUnsupportedMediaType)
        return
    }

    var admissionResponse *admissionv1.AdmissionResponse
    ar := admissionv1.AdmissionReview{}
    if _, _, err := deserializer.Decode(body, nil, &ar); err != nil {
        warningLogger.Printf("Can't decode body: %v", err)
        admissionResponse = &admissionv1.AdmissionResponse{
            Result: &metav1.Status{
                Message: err.Error(),
            },
        }
    } else {
        admissionResponse = whsvr.mutate(&ar)
    }

    admissionReview := admissionv1.AdmissionReview{
        TypeMeta: metav1.TypeMeta{
            APIVersion: "admission.k8s.io/v1",
            Kind:       "AdmissionReview",
        },
    }
    if admissionResponse != nil {
        admissionReview.Response = admissionResponse
        if ar.Request != nil {
            admissionReview.Response.UID = ar.Request.UID
        }
    }

    resp, err := json.Marshal(admissionReview)
    if err != nil {
        warningLogger.Printf("Can't encode response: %v", err)
        http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
    }
    infoLogger.Printf("Writing reponse...")
    if _, err := w.Write(resp); err != nil {
        warningLogger.Printf("Can't write response: %v", err)
        http.Error(w, fmt.Sprintf("could not write response: %v", err), http.StatusInternalServerError)
    }
}
