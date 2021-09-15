// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package clientv3

import (
	"context"

	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/libcalico-go/lib/options"
	validator "github.com/projectcalico/libcalico-go/lib/validator/v3"
	"github.com/projectcalico/libcalico-go/lib/watch"
)

// UISettingsInterface has methods to work with UISettings resources.
type UISettingsInterface interface {
	Create(ctx context.Context, res *apiv3.UISettings, opts options.SetOptions) (*apiv3.UISettings, error)
	Update(ctx context.Context, res *apiv3.UISettings, opts options.SetOptions) (*apiv3.UISettings, error)
	Delete(ctx context.Context, name string, opts options.DeleteOptions) (*apiv3.UISettings, error)
	Get(ctx context.Context, name string, opts options.GetOptions) (*apiv3.UISettings, error)
	List(ctx context.Context, opts options.ListOptions) (*apiv3.UISettingsList, error)
	Watch(ctx context.Context, opts options.ListOptions) (watch.Interface, error)
}

// UISettings implements UISettingsInterface
type UISettings struct {
	client client
}

// Create takes the representation of a UISettings and creates it.  Returns the stored
// representation of the UISettings, and an error, if there is any.
func (r UISettings) Create(
	ctx context.Context, res *apiv3.UISettings, opts options.SetOptions,
) (*apiv3.UISettings, error) {
	if err := validator.Validate(res); err != nil {
		return nil, err
	}
	out, err := r.client.resources.Create(ctx, opts, apiv3.KindUISettings, res)
	if out != nil {
		return out.(*apiv3.UISettings), err
	}
	return nil, err
}

// Update takes the representation of a UISettings and updates it. Returns the stored
// representation of the UISettings, and an error, if there is any.
func (r UISettings) Update(
	ctx context.Context, res *apiv3.UISettings, opts options.SetOptions,
) (*apiv3.UISettings, error) {
	if err := validator.Validate(res); err != nil {
		return nil, err
	}
	out, err := r.client.resources.Update(ctx, opts, apiv3.KindUISettings, res)
	if out != nil {
		return out.(*apiv3.UISettings), err
	}
	return nil, err
}

// Delete takes name of the UISettings and deletes it. Returns an error if one occurs.
func (r UISettings) Delete(
	ctx context.Context, name string, opts options.DeleteOptions,
) (*apiv3.UISettings, error) {
	out, err := r.client.resources.Delete(ctx, opts, apiv3.KindUISettings, noNamespace, name)
	if out != nil {
		return out.(*apiv3.UISettings), err
	}
	return nil, err
}

// Get takes name of the UISettings, and returns the corresponding UISettings object,
// and an error if there is any.
func (r UISettings) Get(
	ctx context.Context, name string, opts options.GetOptions,
) (*apiv3.UISettings, error) {
	out, err := r.client.resources.Get(ctx, opts, apiv3.KindUISettings, noNamespace, name)
	if out != nil {
		return out.(*apiv3.UISettings), err
	}
	return nil, err
}

// List returns the list of UISettings objects that match the supplied options.
func (r UISettings) List(
	ctx context.Context, opts options.ListOptions,
) (*apiv3.UISettingsList, error) {
	res := &apiv3.UISettingsList{}
	if err := r.client.resources.List(
		ctx, opts, apiv3.KindUISettings, apiv3.KindUISettingsList, res,
	); err != nil {
		return nil, err
	}
	return res, nil
}

// Watch returns a watch.Interface that watches the UISettings that match the
// supplied options.
func (r UISettings) Watch(
	ctx context.Context, opts options.ListOptions,
) (watch.Interface, error) {
	return r.client.resources.Watch(ctx, opts, apiv3.KindUISettings, nil)
}
