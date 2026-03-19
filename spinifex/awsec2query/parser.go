package awsec2query

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

/*

Sample args

args["Action"] = "RunInstance"
args["ImageId"] = "ami-123456789"
args["MinCount"] = "1"
args["MaxCount"] = "1"
args["InstanceType"] = "t2.micro"
args["KeyName"] = "my-key-pair"
args["SecurityGroup.1"] = "sg-12345678"
args["SecurityGroup.2"] = "sg-87654321"
args["TagSpecification.1.ResourceType"] = "instance"
args["TagSpecification.1.Tag.1.Key"] = "Name"
args["TagSpecification.1.Tag.1.Value"] = "MyInstance"
args["TagSpecification.2.ResourceType"] = "volume"
args["TagSpecification.2.Tag.1.Key"] = "Environment"
args["TagSpecification.2.Tag.1.Value"] = "Production"

*/

func QueryParamsToStruct(params map[string]string, out any) error {
	v := reflect.ValueOf(out)

	// Must be a pointer to a struct
	if v.Kind() != reflect.Pointer {
		return fmt.Errorf("out must be a pointer to a struct")
	}

	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("out must be a pointer to a struct")
	}

	return setStructFields(v, params, "")
}

func setStructFields(v reflect.Value, params map[string]string, prefix string) error {
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		// Skip unexported fields
		if !field.CanSet() {
			continue
		}

		fieldName := fieldType.Name

		// Check for locationName tag (AWS SDK uses this for query parameter names)
		locationName := fieldType.Tag.Get("locationName")

		// Try the field name, locationName, and title-cased locationName.
		// AWS query params use title case (e.g. "ResourceId") but some SDK
		// structs use camelCase locationName (e.g. "resourceId" in DeleteTagsInput
		// vs "ResourceId" in CreateTagsInput).
		queryKeys := []string{prefix + fieldName}
		if locationName != "" && locationName != fieldName {
			queryKeys = append(queryKeys, prefix+locationName)
			titled := strings.ToUpper(locationName[:1]) + locationName[1:]
			if titled != fieldName && titled != locationName {
				queryKeys = append(queryKeys, prefix+titled)
			}
		}

		// Check if this is a simple field (string, int, bool, etc.)
		for _, queryKey := range queryKeys {
			if val, ok := params[queryKey]; ok {
				if err := setFieldValue(field, val); err != nil {
					return fmt.Errorf("error setting field %s: %w", fieldName, err)
				}
				goto nextField
			}
		}

		// Handle pointer to struct (nested structs)
		if field.Kind() == reflect.Pointer && field.Type().Elem().Kind() == reflect.Struct {
			for _, queryKey := range queryKeys {
				// Check if there are any params with this prefix
				hasParams := false
				searchPrefix := queryKey + "."
				for k := range params {
					if strings.HasPrefix(k, searchPrefix) {
						hasParams = true
						break
					}
				}
				if hasParams {
					structPtr := reflect.New(field.Type().Elem())
					if err := setStructFields(structPtr.Elem(), params, searchPrefix); err != nil {
						return fmt.Errorf("error setting nested struct field %s: %w", fieldName, err)
					}
					field.Set(structPtr)
					goto nextField
				}
			}
		}

		// Handle slice fields (e.g., SecurityGroup.1, SecurityGroup.2)
		if field.Kind() == reflect.Slice {
			for _, queryKey := range queryKeys {
				if err := setSliceField(field, params, queryKey); err != nil {
					return fmt.Errorf("error setting slice field %s: %w", fieldName, err)
				}
				if field.Len() > 0 {
					goto nextField
				}
			}
			continue
		}

		// Handle pointer to slice
		if field.Kind() == reflect.Pointer && field.Type().Elem().Kind() == reflect.Slice {
			for _, queryKey := range queryKeys {
				sliceField := reflect.New(field.Type().Elem()).Elem()
				if err := setSliceField(sliceField, params, queryKey); err != nil {
					return fmt.Errorf("error setting pointer to slice field %s: %w", fieldName, err)
				}
				if sliceField.Len() > 0 {
					field.Set(sliceField.Addr())
					goto nextField
				}
			}
			continue
		}

	nextField:
	}

	return nil
}

func setFieldValue(field reflect.Value, value string) error {
	switch field.Kind() {
	case reflect.Slice:
		elem := field.Type().Elem()
		// Special case: []byte
		if elem.Kind() == reflect.Uint8 {
			// First try base64 (what the AWS API expects for PublicKeyMaterial).
			trimmed := strings.TrimSpace(value)
			if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
				field.SetBytes(decoded)
				return nil
			}
			// Some clients may send raw text. Fall back to raw bytes.
			field.SetBytes([]byte(value))
			return nil
		}

	case reflect.String:
		field.SetString(value)
	case reflect.Pointer:
		// Handle pointer types
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		return setFieldValue(field.Elem(), value)
	case reflect.Int, reflect.Int64:
		i, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		field.SetInt(i)
	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		field.SetBool(b)
	default:
		return fmt.Errorf("unsupported field type: %v", field.Kind())
	}
	return nil
}

func setSliceField(field reflect.Value, params map[string]string, prefix string) error {
	// Find all indexed items for this slice
	indices := make(map[int]bool)
	for key := range params {
		if strings.HasPrefix(key, prefix+".") {
			parts := strings.Split(key[len(prefix)+1:], ".")
			if len(parts) > 0 {
				if idx, err := strconv.Atoi(parts[0]); err == nil {
					indices[idx] = true
				}
			}
		}
	}

	if len(indices) == 0 {
		return nil
	}

	// Create slice with appropriate size
	maxIdx := 0
	for idx := range indices {
		if idx > maxIdx {
			maxIdx = idx
		}
	}

	elemType := field.Type().Elem()
	slice := reflect.MakeSlice(field.Type(), maxIdx, maxIdx)

	// Process each index
	for idx := 1; idx <= maxIdx; idx++ {
		if !indices[idx] {
			continue
		}

		elem := slice.Index(idx - 1)
		indexPrefix := fmt.Sprintf("%s.%d", prefix, idx)

		// Handle different element types
		switch elemType.Kind() {
		case reflect.String:
			if val, ok := params[indexPrefix]; ok {
				elem.SetString(val)
			}
		case reflect.Pointer:
			// Handle pointer to string
			if elemType.Elem().Kind() == reflect.String {
				if val, ok := params[indexPrefix]; ok {
					str := val
					elem.Set(reflect.ValueOf(&str))
				}
			} else if elemType.Elem().Kind() == reflect.Struct {
				// Handle pointer to struct
				structPtr := reflect.New(elemType.Elem())
				if err := setStructFields(structPtr.Elem(), params, indexPrefix+"."); err != nil {
					return err
				}
				elem.Set(structPtr)
			}
		case reflect.Struct:
			if err := setStructFields(elem, params, indexPrefix+"."); err != nil {
				return err
			}
		default:
			if val, ok := params[indexPrefix]; ok {
				if err := setFieldValue(elem, val); err != nil {
					return err
				}
			}
		}
	}

	field.Set(slice)
	return nil
}
