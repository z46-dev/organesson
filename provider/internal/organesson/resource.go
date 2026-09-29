package organesson

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type resourceDescriptionFunc func(*schema.ResourceData) (string, error)

// localResource defines a parsed-only resource with shared local lifecycle behavior.
func localResource(resourceType string, fields map[string]*schema.Schema, describe resourceDescriptionFunc) (resource *schema.Resource) {
	resource = &schema.Resource{
		CreateContext: localResourceCreate(resourceType, describe),
		ReadContext:   localResourceRead,
		UpdateContext: localResourceUpdate(resourceType, describe),
		DeleteContext: localResourceDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		Schema: fields,
	}

	return
}

// localResourceCreate logs a parsed resource without changing Organesson or PVE.
func localResourceCreate(resourceType string, describe resourceDescriptionFunc) schema.CreateContextFunc {
	return func(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
		var (
			description string
			err         error
		)

		if description, err = describe(data); err != nil {
			diagnostics = diag.FromErr(err)

			return
		}

		log.Printf("[INFO] Organesson parsed resource: %s", description)

		if err = data.Set("summary", description); err != nil {
			diagnostics = diag.FromErr(err)

			return
		}

		data.SetId(localResourceID(resourceType, description))

		return
	}
}

// localResourceRead preserves parsed-only provider state.
func localResourceRead(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
	return
}

// localResourceUpdate logs the revised parsed resource without changing infrastructure.
func localResourceUpdate(resourceType string, describe resourceDescriptionFunc) schema.UpdateContextFunc {
	return func(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
		diagnostics = localResourceCreate(resourceType, describe)(ctx, data, meta)

		return
	}
}

// localResourceDelete logs the future destruction boundary without changing infrastructure.
func localResourceDelete(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
	log.Printf("[INFO] Organesson would delete parsed resource %q", data.Id())
	data.SetId("")

	return
}

// localResourceID creates a deterministic local state identifier until the API assigns one.
func localResourceID(resourceType string, description string) (id string) {
	var digest [sha256.Size]byte = sha256.Sum256([]byte(description))

	id = fmt.Sprintf("local:%s:%s", resourceType, hex.EncodeToString(digest[:8]))

	return
}

// requiredStringSchema returns a required string field.
func requiredStringSchema(description string) (field *schema.Schema) {
	field = &schema.Schema{Type: schema.TypeString, Required: true, Description: description}

	return
}

// optionalStringSchema returns an optional string field.
func optionalStringSchema(description string) (field *schema.Schema) {
	field = &schema.Schema{Type: schema.TypeString, Optional: true, Description: description}

	return
}

// requiredIntSchema returns a required integer field.
func requiredIntSchema(description string) (field *schema.Schema) {
	field = &schema.Schema{Type: schema.TypeInt, Required: true, Description: description}

	return
}

// summarySchema returns the computed parsed-resource summary field.
func summarySchema() (field *schema.Schema) {
	field = &schema.Schema{Type: schema.TypeString, Computed: true, Description: "The parsed resource action."}

	return
}
