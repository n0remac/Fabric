package fabric

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var bindingSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

var (
	ErrInvalidBinding = errors.New("invalid binding")
	ErrBindingMissing = errors.New("binding was not found")
	ErrBindingObject  = errors.New("binding traverses a non-object value")
)

func ValidateBinding(path string) error {
	if path == "" {
		return fmt.Errorf("%w: path is empty", ErrInvalidBinding)
	}
	for _, segment := range strings.Split(path, ".") {
		if !bindingSegmentPattern.MatchString(segment) {
			return fmt.Errorf("%w: invalid segment %q", ErrInvalidBinding, segment)
		}
	}
	return nil
}

func ResolveBinding(data map[string]any, path string) (any, error) {
	if err := ValidateBinding(path); err != nil {
		return nil, err
	}
	var current any = data
	segments := strings.Split(path, ".")
	for index, segment := range segments {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w at %q", ErrBindingObject, strings.Join(segments[:index], "."))
		}
		next, exists := object[segment]
		if !exists {
			return nil, fmt.Errorf("%w: %s", ErrBindingMissing, path)
		}
		current = next
	}
	return current, nil
}
