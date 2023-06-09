package templatesync

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// unmarshalFromJSON unmarshals raw JSON data into an object
func unmarshalFromJSON(rawData []byte) (unstructured.Unstructured, error) {
	var unstruct unstructured.Unstructured

	if jsonErr := json.Unmarshal(rawData, &unstruct.Object); jsonErr != nil {
		log.Error(jsonErr, "Could not unmarshal data from JSON")

		return unstruct, jsonErr
	}

	return unstruct, nil
}
