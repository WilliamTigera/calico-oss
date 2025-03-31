// Copyright (c) 2025 Tigera, Inc. All rights reserved.

package fake

import (
	"context"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator"
)

type AnyLog struct {
	ID string
}
type ReadCommand struct {
	Data *v1.List[AnyLog]
	Next *operator.TimeInterval
}
type Operator struct {
	idx          int
	readCommands []ReadCommand
}

func (f *Operator) AddReadCommand(readCommand ...ReadCommand) {
	f.readCommands = append(f.readCommands, readCommand...)
}

func (f *Operator) Write(ctx context.Context, items []AnyLog) (*v1.BulkResponse, error) {
	return &v1.BulkResponse{
		Total:     len(items),
		Succeeded: len(items),
		Failed:    0,
	}, nil
}

func (f *Operator) Read(ctx context.Context, current operator.TimeInterval, pageSize int) (*v1.List[AnyLog], *operator.TimeInterval, error) {
	if f.idx < len(f.readCommands) {
		readCommand := f.readCommands[f.idx]
		f.idx++
		return readCommand.Data, readCommand.Next, nil
	}
	return &v1.List[AnyLog]{}, &operator.TimeInterval{}, nil
}

func (f Operator) Transform(items []AnyLog) []string {
	var result []string
	for _, item := range items {
		result = append(result, item.ID)
	}
	return result
}
