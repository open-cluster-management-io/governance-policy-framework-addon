package templatesync

import (
	"context"
	"encoding/json"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
)

// unmarshalFromJSON unmarshals raw JSON data into an object
func unmarshalFromJSON(ctx context.Context, rawData []byte) (unstructured.Unstructured, error) {
	log := ctrl.LoggerFrom(ctx)

	var unstruct unstructured.Unstructured

	if jsonErr := json.Unmarshal(rawData, &unstruct.Object); jsonErr != nil {
		log.Error(jsonErr, "Could not unmarshal data from JSON")

		return unstruct, jsonErr
	}

	return unstruct, nil
}
