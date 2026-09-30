package organesson

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type (
	deploymentResult struct {
		Deployment struct {
			ID         int  `json:"id"`
			RootNodeID *int `json:"root_node_id"`
		} `json:"deployment"`
	}
	nodeResult struct {
		Node struct {
			ID           int    `json:"id"`
			DeploymentID int    `json:"deployment_id"`
			ParentID     *int   `json:"parent_id"`
			Name         string `json:"name"`
		} `json:"ownership_node"`
	}
	vmResult struct {
		Resource struct {
			ID          int    `json:"id"`
			OwnershipID int    `json:"ownership_id"`
			Name        string `json:"name"`
			PowerState  string `json:"power_state"`
		} `json:"resource"`
	}
	userGroupResult struct {
		Group struct {
			ID           int    `json:"id"`
			DeploymentID int    `json:"deployment_id"`
			Name         string `json:"name"`
		} `json:"user_group"`
		Members []string `json:"members"`
	}
	grantResult struct {
		Grant struct {
			ID                 int    `json:"id"`
			SubjectKind        int    `json:"subject_kind"`
			SubjectID          int    `json:"subject_id"`
			Permission         string `json:"permission"`
			TargetNodeID       int    `json:"target_node_id"`
			InheritDescendants bool   `json:"inherit_descendants"`
		} `json:"permission_grant"`
		SubjectName string `json:"subject_name"`
	}
)

// deploymentOperations supplies the API lifecycle for a managed deployment root.
func deploymentOperations() (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var result deploymentResult
		if err = client.request(ctx, http.MethodPost, "/api/v1/deployments", map[string]string{
			"name":        data.Get("name").(string),
			"description": data.Get("description").(string),
		}, &result); err != nil {
			return
		}
		if result.Deployment.ID < 1 || result.Deployment.RootNodeID == nil {
			return fmt.Errorf("Organesson returned an incomplete deployment")
		}
		data.SetId(strconv.Itoa(result.Deployment.ID))
		_ = data.Set("root_node_id", *result.Deployment.RootNodeID)
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result deploymentResult
		err = client.request(ctx, http.MethodGet, "/api/v1/deployments/"+id, nil, &result)
		if err == nil && result.Deployment.RootNodeID != nil {
			_ = data.Set("root_node_id", *result.Deployment.RootNodeID)
		}
		return
	}
	operations.Update = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result deploymentResult
		err = client.request(ctx, http.MethodPut, "/api/v1/deployments/"+id, map[string]string{
			"name":        data.Get("name").(string),
			"description": data.Get("description").(string),
		}, &result)
		return
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/deployments/"+id, nil, nil)
	}
	return
}

// logicalGroupOperations supplies remote ownership-node operations.
func logicalGroupOperations() (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var deploymentID string
		if deploymentID, err = remoteID(data.Get("deployment_id").(string)); err != nil {
			return
		}
		var result nodeResult
		err = client.request(ctx, http.MethodPost, "/api/v1/deployments/"+deploymentID+"/logical-groups", map[string]any{
			"name":           data.Get("name").(string),
			"parent_node_id": data.Get("parent_node_id").(int),
		}, &result)
		if err != nil {
			return
		}
		data.SetId(strconv.Itoa(result.Node.ID))
		_ = data.Set("deployment_id", strconv.Itoa(result.Node.DeploymentID))
		_ = data.Set("parent_node_id", parentID(result.Node.ParentID))
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result nodeResult
		err = client.request(ctx, http.MethodGet, "/api/v1/ownership-nodes/"+id, nil, &result)
		if err == nil {
			_ = data.Set("deployment_id", strconv.Itoa(result.Node.DeploymentID))
			_ = data.Set("name", result.Node.Name)
			_ = data.Set("parent_node_id", parentID(result.Node.ParentID))
		}
		return
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/ownership-nodes/"+id, nil, nil)
	}
	return
}

// userGroupOperations creates deployment-local groups and their initial memberships.
func userGroupOperations() (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var deploymentID string
		if deploymentID, err = remoteID(data.Get("deployment_id").(string)); err != nil {
			return
		}
		var result userGroupResult
		err = client.request(ctx, http.MethodPost, "/api/v1/deployments/"+deploymentID+"/user-groups", map[string]any{
			"name":    data.Get("name").(string),
			"members": stringSetValues(data.Get("members").(*schema.Set)),
		}, &result)
		if err != nil {
			return
		}
		data.SetId(strconv.Itoa(result.Group.ID))
		_ = data.Set("deployment_id", strconv.Itoa(result.Group.DeploymentID))
		_ = data.Set("members", result.Members)
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result userGroupResult
		err = client.request(ctx, http.MethodGet, "/api/v1/user-groups/"+id, nil, &result)
		if err == nil {
			_ = data.Set("deployment_id", strconv.Itoa(result.Group.DeploymentID))
			_ = data.Set("name", result.Group.Name)
			_ = data.Set("members", result.Members)
		}
		return
	}
	operations.Update = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodPut, "/api/v1/user-groups/"+id+"/members", map[string][]string{
			"members": stringSetValues(data.Get("members").(*schema.Set)),
		}, nil)
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/user-groups/"+id, nil, nil)
	}
	return
}

// virtualMachineOperations manages the backend's simulated VM resource records.
func virtualMachineOperations() (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var parentID string
		if parentID, err = remoteID(data.Get("logical_group_id").(string)); err != nil {
			return
		}
		var parentResult nodeResult
		if err = client.request(ctx, http.MethodGet, "/api/v1/ownership-nodes/"+parentID, nil, &parentResult); err != nil {
			return
		}
		var result vmResult
		err = client.request(ctx, http.MethodPost, "/api/v1/deployments/"+strconv.Itoa(parentResult.Node.DeploymentID)+"/virtual-machines", map[string]any{
			"parent_node_id": parentResult.Node.ID,
			"name":           data.Get("name").(string),
		}, &result)
		if err == nil {
			data.SetId(strconv.Itoa(result.Resource.ID))
			_ = data.Set("ownership_node_id", result.Resource.OwnershipID)
			_ = data.Set("power_state", result.Resource.PowerState)
		}
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result vmResult
		err = client.request(ctx, http.MethodGet, "/api/v1/virtual-machines/"+id, nil, &result)
		if err == nil {
			_ = data.Set("name", result.Resource.Name)
			_ = data.Set("ownership_node_id", result.Resource.OwnershipID)
			_ = data.Set("power_state", result.Resource.PowerState)
		}
		return
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/virtual-machines/"+id, nil, nil)
	}
	return
}

// permissionGrantOperations manages fixed account and group grants in the ownership tree.
func permissionGrantOperations() (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var targetID string
		if targetID, err = remoteID(data.Get("target_id").(string)); err != nil {
			return
		}
		var subject string = data.Get("subject_id").(string)
		var subjectKind int
		var subjectID int
		var subjectName string
		if subjectID, err = strconv.Atoi(subject); err == nil {
			subjectKind = 1
		} else {
			subjectKind = 0
			subjectName = subject
		}
		var result grantResult
		err = client.request(ctx, http.MethodPost, "/api/v1/ownership-nodes/"+targetID+"/grants", map[string]any{
			"subject_kind":        subjectKind,
			"subject_id":          subjectID,
			"subject_name":        subjectName,
			"permission":          data.Get("permission").(string),
			"inherit_descendants": data.Get("scope").(string) == "descendants",
		}, &result)
		if err == nil {
			data.SetId(strconv.Itoa(result.Grant.ID))
			_ = data.Set("subject_id", grantSubjectID(result))
			_ = data.Set("target_id", strconv.Itoa(result.Grant.TargetNodeID))
			_ = data.Set("scope", grantScope(result.Grant.InheritDescendants))
		}
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result grantResult
		err = client.request(ctx, http.MethodGet, "/api/v1/permission-grants/"+id, nil, &result)
		if err == nil {
			_ = data.Set("permission", result.Grant.Permission)
			_ = data.Set("subject_id", grantSubjectID(result))
			_ = data.Set("target_id", strconv.Itoa(result.Grant.TargetNodeID))
			_ = data.Set("scope", grantScope(result.Grant.InheritDescendants))
		}
		return
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/permission-grants/"+id, nil, nil)
	}
	return
}

// remoteID validates the numeric resource identifiers used by this API slice.
func remoteID(value string) (id string, err error) {
	var parsed int
	if parsed, err = strconv.Atoi(strings.TrimSpace(value)); err != nil || parsed < 1 {
		err = errRemoteNotFound
		return
	}
	id = strconv.Itoa(parsed)
	return
}

func parentID(value *int) (id int) {
	if value != nil {
		id = *value
	}
	return
}

func grantSubjectID(result grantResult) (id string) {
	if result.Grant.SubjectKind == 0 {
		id = result.SubjectName
	} else {
		id = strconv.Itoa(result.Grant.SubjectID)
	}
	return
}

func grantScope(inheritDescendants bool) (scope string) {
	scope = "self"
	if inheritDescendants {
		scope = "descendants"
	}
	return
}
