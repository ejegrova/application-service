/*
Copyright 2021 Red Hat, Inc.

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

package controllers

import (
	"context"
	"fmt"
	"net/url"

	appstudiov1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
	"github.com/redhat-appstudio/application-service/gitops"
	devfile "github.com/redhat-appstudio/application-service/pkg/devfile"
	"github.com/redhat-appstudio/application-service/pkg/util/ioutils"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"
)

const compFinalizerName = "component.appstudio.redhat.com/finalizer"

// AddFinalizer adds the finalizer to the Component CR
func (r *ComponentReconciler) AddFinalizer(ctx context.Context, component *appstudiov1alpha1.Component) error {
	controllerutil.AddFinalizer(component, compFinalizerName)
	return r.Update(ctx, component)
}

// Finalize deletes the corresponding devfile project or the devfile attribute entry from the Application CR and also deletes the corresponding GitOps repo's Component dir
// & updates the parent kustomize for the given Component CR.
func (r *ComponentReconciler) Finalize(ctx context.Context, component *appstudiov1alpha1.Component, application *appstudiov1alpha1.Application) error {
	// Get the Application CR devfile
	devfileObj, err := devfile.ParseDevfileModel(application.Status.Devfile)
	if err != nil {
		return err
	}

	if component.Spec.Source.GitSource != nil {
		err = devfileObj.DeleteProject(component.Spec.ComponentName)
		if err != nil {
			return err
		}
	} else if component.Spec.ContainerImage != "" {
		devSpec := devfileObj.GetDevfileWorkspaceSpec()
		if devSpec != nil {
			attributes := devSpec.Attributes
			delete(attributes, fmt.Sprintf("containerImage/%s", component.Spec.ComponentName))
			devSpec.Attributes = attributes
			devfileObj.SetDevfileWorkspaceSpec(*devSpec)
		}

	}

	yamldevfileObj, err := yaml.Marshal(devfileObj)
	if err != nil {
		return nil
	}

	application.Status.Devfile = string(yamldevfileObj)

	gitopsStatus := component.Status.GitOps

	// Get the information about the gitops repository from the Component resource
	var gitOpsURL, gitOpsBranch, gitOpsContext string
	gitOpsURL = gitopsStatus.RepositoryURL
	if gitOpsURL == "" {
		err := fmt.Errorf("did not find any gitOps URL for the component during clean up")
		return err
	}
	if gitopsStatus.Branch != "" {
		gitOpsBranch = gitopsStatus.Branch
	} else {
		gitOpsBranch = "main"
	}
	if gitopsStatus.Context != "" {
		gitOpsContext = gitopsStatus.Context
	} else {
		gitOpsContext = "/"
	}

	// Construct the remote URL for the gitops repository
	parsedURL, err := url.Parse(gitOpsURL)
	if err != nil {
		return err
	}
	parsedURL.User = url.User(r.GitToken)
	remoteURL := parsedURL.String()

	// Create a temp folder to create the gitops resources in
	tempDir, err := ioutils.CreateTempPath(component.Name, r.AppFS)
	if err != nil {
		return fmt.Errorf("unable to create temp directory for gitops resources due to error: %v", err)
	}

	err = gitops.RemoveAndPush(tempDir, remoteURL, *component, r.Executor, r.AppFS, gitOpsBranch, gitOpsContext)
	if err != nil {
		return err
	}

	err = r.AppFS.RemoveAll(tempDir)
	if err != nil {
		return err
	}

	return r.Status().Update(ctx, application)
}
