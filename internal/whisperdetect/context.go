package whisperdetect

import "context"

type sourceKey struct{}

// Source returns the detection result for the current request, waiting
// briefly for the end of the utterance if the microphone stream is still open.
type Source func(context.Context) Result

// WithSource attaches a detector to a request context.
func WithSource(ctx context.Context, source Source) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

// Detect reports the request's whisper detection, if detection is enabled.
func Detect(ctx context.Context) (Result, bool) {
	source, ok := ctx.Value(sourceKey{}).(Source)
	if !ok || source == nil {
		return Result{}, false
	}
	return source(ctx), true
}
