package fsops

import (
	"context"
	"errors"
	"reflect"

	"workcopy/internal/model"
)

// RenderCopy returns a deep copy of value with every absolute path inside its
// strings written in the requested form. The original is not touched: reports
// can share memory with state that outlives the call (the MCP server is long
// lived), so converting in place is not safe.
//
// Only exported struct fields are copied, which is every field of the report and
// tool-output types. Map keys are left as they are: they are lane-relative
// names, never absolute paths.
func RenderCopy[T any](value T, form PathForm) T {
	if form == FormNative {
		return value
	}
	return renderCopy(reflect.ValueOf(&value).Elem(), form, currentEnvironment(), wslMountRoot()).Interface().(T)
}

func renderCopy(value reflect.Value, form PathForm, env environment, mountRoot string) reflect.Value {
	switch value.Kind() {
	case reflect.String:
		out := reflect.New(value.Type()).Elem()
		out.SetString(renderText(value.String(), form, env, mountRoot))
		return out
	case reflect.Pointer:
		if value.IsNil() {
			return value
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(renderCopy(value.Elem(), form, env, mountRoot))
		return out
	case reflect.Interface:
		if value.IsNil() {
			return value
		}
		out := reflect.New(value.Type()).Elem()
		out.Set(renderCopy(value.Elem(), form, env, mountRoot))
		return out
	case reflect.Slice:
		if value.IsNil() {
			return value
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			out.Index(index).Set(renderCopy(value.Index(index), form, env, mountRoot))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			out.Index(index).Set(renderCopy(value.Index(index), form, env, mountRoot))
		}
		return out
	case reflect.Map:
		if value.IsNil() {
			return value
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		for _, key := range value.MapKeys() {
			out.SetMapIndex(key, renderCopy(value.MapIndex(key), form, env, mountRoot))
		}
		return out
	case reflect.Struct:
		out := reflect.New(value.Type()).Elem()
		for index := 0; index < value.NumField(); index++ {
			if value.Type().Field(index).IsExported() {
				out.Field(index).Set(renderCopy(value.Field(index), form, env, mountRoot))
			}
		}
		return out
	}
	return value
}

// RenderError writes the paths in an error's message in the requested form while
// keeping what callers test for: a *model.Failure stays one, and a cancellation
// is still a cancellation.
func RenderError(err error, form PathForm) error {
	if err == nil || form == FormNative {
		return err
	}
	var failure *model.Failure
	if errors.As(err, &failure) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		rendered := *failure
		rendered.Message = RenderText(failure.Message, form)
		rendered.Suggestion = RenderText(failure.Suggestion, form)
		return &rendered
	}
	return &renderedError{message: RenderText(err.Error(), form), err: err}
}

type renderedError struct {
	message string
	err     error
}

func (e *renderedError) Error() string { return e.message }
func (e *renderedError) Unwrap() error { return e.err }
