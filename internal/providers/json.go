package providers

import "encoding/json"

func jsonUnmarshal(data []byte, target interface{}) error {
	return json.Unmarshal(data, target)
}
