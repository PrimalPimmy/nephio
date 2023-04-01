/*
 Copyright 2023 Nephio.

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

package kptrl

import (
	"github.com/GoogleContainerTools/kpt-functions-sdk/go/fn"
)

type ResourceList interface {
	AddResult(err error, obj *fn.KubeObject)
	GetObjects() fn.KubeObjects
	SetObject(obj *fn.KubeObject)
	AddObject(obj *fn.KubeObject)
	DeleteObject(obj *fn.KubeObject)
}

func New(rl *fn.ResourceList) ResourceList {
	return &resourceList{
		rl: rl,
	}
}

type resourceList struct {
	rl *fn.ResourceList
}

func (r *resourceList) AddResult(err error, obj *fn.KubeObject) {
	r.rl.Results = append(r.rl.Results, fn.ErrorConfigObjectResult(err, obj))
}

func (r *resourceList) GetObjects() fn.KubeObjects {
	return r.rl.Items
}

func (r *resourceList) SetObject(obj *fn.KubeObject) {
	exists := false
	for idx, o := range r.rl.Items {
		if IsGVKN(o, obj) {
			r.rl.Items[idx] = obj
			exists = true
			break
		}
	}
	if !exists {
		r.AddObject(obj)
	}
}

func (r *resourceList) AddObject(obj *fn.KubeObject) {
	r.rl.Items = append(r.rl.Items, obj)
}

func (r *resourceList) DeleteObject(obj *fn.KubeObject) {
	for idx, o := range r.rl.Items {
		if IsGVKN(o, obj) {
			r.rl.Items = append(r.rl.Items[:idx], r.rl.Items[idx+1:]...)
		}
	}
}

func IsGVKN(co, no *fn.KubeObject) bool {
	if co.GetAPIVersion() == no.GetAPIVersion() && co.GetKind() == no.GetKind() && co.GetName() == no.GetName() {
		return true
	}
	return false
}
