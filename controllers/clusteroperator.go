/*

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
	"os"
	"strings"

	"github.com/pkg/errors"

	osconfigv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StatusReason is a MixedCaps string representing the reason for a
// status condition change.
type StatusReason string

const (
	clusterOperatorName = "baremetal"

	// OperatorDisabled represents a Disabled ClusterStatusConditionTypes
	OperatorDisabled osconfigv1.ClusterStatusConditionType = "Disabled"

	// ReasonEmpty is an empty StatusReason
	ReasonEmpty StatusReason = ""
	// ReasonComplete signals status completion
	ReasonComplete StatusReason = "DeployComplete"
	// ReasonSyncing indicates that we are currently syncing
	ReasonSyncing StatusReason = "SyncingResources"
	// ReasonSyncFailed means we failed while syncing
	ReasonSyncFailed StatusReason = "SyncingFailed"
	// ReasonUnsupported means we have an unsupported platform
	ReasonUnsupported StatusReason = "UnsupportedPlatform"
)

// defaultStatusConditions returns the default set of status conditions for the
// ClusterOperator resource used on first creation of the ClusterOperator.
func defaultStatusConditions() []osconfigv1.ClusterOperatorStatusCondition {
	return []osconfigv1.ClusterOperatorStatusCondition{
		setStatusCondition(osconfigv1.OperatorProgressing, osconfigv1.ConditionFalse, "", ""),
		setStatusCondition(osconfigv1.OperatorDegraded, osconfigv1.ConditionFalse, "", ""),
		setStatusCondition(osconfigv1.OperatorAvailable, osconfigv1.ConditionFalse, "", ""),
		setStatusCondition(osconfigv1.OperatorUpgradeable, osconfigv1.ConditionTrue, "", ""),
		setStatusCondition(OperatorDisabled, osconfigv1.ConditionFalse, "", ""),
	}
}

// relatedObjects returns the current list of ObjectReference's for the
// ClusterOperator objects's status.
func relatedObjects() []osconfigv1.ObjectReference {
	return []osconfigv1.ObjectReference{
		{
			Group:    "",
			Resource: "namespaces",
			Name:     ComponentNamespace,
		},
	}
}

// createClusterOperator creates the ClusterOperator and updates its status.
func (r *ProvisioningReconciler) createClusterOperator() (*osconfigv1.ClusterOperator, error) {
	defaultCO := &osconfigv1.ClusterOperator{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterOperator",
			APIVersion: "config.openshift.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterOperatorName,
		},
		Status: osconfigv1.ClusterOperatorStatus{
			Conditions:     defaultStatusConditions(),
			RelatedObjects: relatedObjects(),
		},
	}
	//operatorv1helpers.SetOperandVersion(&defaultCO.Status.Versions, osconfigv1.OperandVersion{Name: "operator", Version: os.Getenv("RELEASE_VERSION")})

	co, err := r.OSClient.ConfigV1().ClusterOperators().Create(context.Background(), defaultCO, metav1.CreateOptions{})
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to create ClusterOperator %s",
			clusterOperatorName))
	}
	r.Log.V(1).Info("created ClusterOperator", "name", clusterOperatorName)

	co.Status = defaultCO.Status
	return r.OSClient.ConfigV1().ClusterOperators().UpdateStatus(context.Background(), co, metav1.UpdateOptions{})
}

// getOrCreateClusterOperator gets the existing CO, failing which it creates a new CO.
func (r *ProvisioningReconciler) getOrCreateClusterOperator() (*osconfigv1.ClusterOperator, error) {
	existing, err := r.OSClient.ConfigV1().ClusterOperators().Get(context.Background(), clusterOperatorName, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		return r.createClusterOperator()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get clusterOperator %q: %v", clusterOperatorName, err)
	}

	return existing, nil
}

// setStatusCondition initalizes and returns a ClusterOperatorStatusCondition
func setStatusCondition(conditionType osconfigv1.ClusterStatusConditionType,
	conditionStatus osconfigv1.ConditionStatus, reason string,
	message string) osconfigv1.ClusterOperatorStatusCondition {
	return osconfigv1.ClusterOperatorStatusCondition{
		Type:               conditionType,
		Status:             conditionStatus,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

// getOperandVersions returns the operand version
func (r *ProvisioningReconciler) getOperandVersions() []osconfigv1.OperandVersion {
	operandVersions := []osconfigv1.OperandVersion{}
	if releaseVersion := os.Getenv("RELEASE_VERSION"); len(releaseVersion) > 0 {
		operandVersions = append(operandVersions, osconfigv1.OperandVersion{Name: "operator", Version: releaseVersion})
	} else {
		err := fmt.Errorf("env variable: RELEASE_VERSION was not set")
		r.Log.Error(err, "failed to get OperandVersion")
	}
	return operandVersions
}

//syncStatus applies the new condition to the CBO ClusterOperator object.
func (r *ProvisioningReconciler) syncStatus(co *osconfigv1.ClusterOperator, conds []osconfigv1.ClusterOperatorStatusCondition) error {
	for _, c := range conds {
		v1helpers.SetStatusCondition(&co.Status.Conditions, c)
	}

	_, err := r.OSClient.ConfigV1().ClusterOperators().UpdateStatus(context.Background(), co, metav1.UpdateOptions{})
	return err
}

// updateCOStatusDisabled updates the ClusterOperator's status to Disabled
func (r *ProvisioningReconciler) updateCOStatusDisabled() error {
	disabledMessage := "Operator is non functional"
	availableMessage := "Operator is available while being disabled"

	co, err := r.getOrCreateClusterOperator()
	if err != nil {
		r.Log.Error(err, "failed to get or create ClusterOperator")
		return err
	}

	conds := []osconfigv1.ClusterOperatorStatusCondition{
		setStatusCondition(osconfigv1.OperatorAvailable, osconfigv1.ConditionTrue, string(ReasonUnsupported), availableMessage),
		setStatusCondition(OperatorDisabled, osconfigv1.ConditionTrue, string(ReasonUnsupported), disabledMessage),
	}

	return r.syncStatus(co, conds)
}

// updateCOStatusDegraded updates the ClusterOperator's Degraded
// degradedReason should contain the current reason for the Operator to be marked in that state
func (r *ProvisioningReconciler) updateCOStatusDegraded(degradedReason string, detailedError string) error {
	degradedMessage := "Operator is Degraded"
	progressingMessage := "Operator is Degraded while Progressing"

	co, err := r.getOrCreateClusterOperator()
	if err != nil {
		return err
	}

	conds := []osconfigv1.ClusterOperatorStatusCondition{
		setStatusCondition(osconfigv1.OperatorDegraded, osconfigv1.ConditionTrue, degradedReason, degradedMessage),
		setStatusCondition(osconfigv1.OperatorProgressing, osconfigv1.ConditionTrue, detailedError, progressingMessage),
		setStatusCondition(osconfigv1.OperatorAvailable, osconfigv1.ConditionFalse, "", ""),
	}

	return r.syncStatus(co, conds)
}

// updateCOStatusAvailable updates the ClusterOperator's status to Available
func (r *ProvisioningReconciler) updateCOStatusAvailable() error {
	co, err := r.getOrCreateClusterOperator()
	if err != nil {
		return err
	}

	// Write the operand versions when available
	co.Status.Versions = r.getOperandVersions()

	versionsOutput := []string{}
	for _, operand := range co.Status.Versions {
		versionsOutput = append(versionsOutput, fmt.Sprintf("%s: %s", operand.Name, operand.Version))
	}
	versions := strings.Join(versionsOutput, ", ")

	conds := []osconfigv1.ClusterOperatorStatusCondition{
		setStatusCondition(osconfigv1.OperatorAvailable, osconfigv1.ConditionTrue, string(ReasonEmpty),
			fmt.Sprintf("Cluster Baremetal Operator is available at %s", versions)),
		setStatusCondition(osconfigv1.OperatorProgressing, osconfigv1.ConditionFalse, "", ""),
		setStatusCondition(osconfigv1.OperatorDegraded, osconfigv1.ConditionFalse, "", ""),
		setStatusCondition(osconfigv1.OperatorUpgradeable, osconfigv1.ConditionTrue, "", ""),
		setStatusCondition(OperatorDisabled, osconfigv1.ConditionFalse, "", ""),
	}

	return r.syncStatus(co, conds)
}
