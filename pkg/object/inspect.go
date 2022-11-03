package object

import (
	"reflect"

	"github.com/pkg/errors"
)

func IsEmpty(object interface{}) (bool, error) {
	// First check normal definitions of empty
	if object == nil {
		return true, nil
	} else if object == "" {
		return true, nil
	} else if object == false {
		return true, nil
	}

	// Then see if it's a struct
	if reflect.ValueOf(object).Kind() == reflect.Struct {
		// and create an empty copy of the struct object to compare against
		empty := reflect.New(reflect.TypeOf(object)).Elem().Interface()
		if reflect.DeepEqual(object, empty) {
			return true, nil
		} else {
			return false, nil
		}
	}

	return false, errors.New("Check not implementend for this struct")
}
