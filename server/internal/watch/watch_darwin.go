package watch

import "context"

func watchTree(ctx context.Context, root string, fn Func, ready func()) error {
	return pollTree(ctx, root, fn, ready)
}
