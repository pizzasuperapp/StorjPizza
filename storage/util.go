// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package storage

import (
	"context"
	"fmt"
)

// NextKey returns the successive key.
func NextKey(key Key) Key {
	return append(CloneKey(key), 0)
}

// AfterPrefix returns the key after prefix.
func AfterPrefix(key Key) Key {
	after := CloneKey(key)
	after[len(after)-1]++
	return after
}

// CloneKey creates a copy of key.
func CloneKey(key Key) Key { return append(Key{}, key...) }

// CloneValue creates a copy of value.
func CloneValue(value Value) Value { return append(Value{}, value...) }

// CloneItem creates a deep copy of item.
func CloneItem(item ListItem) ListItem {
	return ListItem{
		Key:      CloneKey(item.Key),
		Value:    CloneValue(item.Value),
		IsPrefix: item.IsPrefix,
	}
}

// CloneItems creates a deep copy of items.
func CloneItems(items Items) Items {
	var result = make(Items, len(items))
	for i, item := range items {
		result[i] = CloneItem(item)
	}
	return result
}

// PutAll adds multiple values to the store.
func PutAll(ctx context.Context, store KeyValueStore, items ...ListItem) (err error) {
	defer mon.Task()(&ctx)(&err)

	for _, item := range items {
		err := store.Put(ctx, item.Key, item.Value)
		if err != nil {
			return fmt.Errorf("failed to put %v: %w", item, err)
		}
	}
	return nil
}
