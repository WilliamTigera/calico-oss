// Copyright 2019 Tigera Inc. All rights reserved.

package elastic

import "context"

type MockXPack struct {
	Datafeeds      map[string]DatafeedSpec
	DatafeedCounts map[string]DatafeedCountsSpec
	Jobs           map[string]JobSpec
	JobStats       map[string]JobStatsSpec
	Buckets        []BucketSpec
	Records        []RecordSpec
	Ok             bool
	Err            error
}

func (m *MockXPack) GetDatafeeds(ctx context.Context, feedIDs ...string) ([]DatafeedSpec, error) {
	var res []DatafeedSpec
	for _, fid := range feedIDs {
		if s, ok := m.Datafeeds[fid]; ok {
			res = append(res, s)
		}
	}
	return res, m.Err
}

func (m *MockXPack) GetDatafeedStats(ctx context.Context, feedIDs ...string) ([]DatafeedCountsSpec, error) {
	var res []DatafeedCountsSpec
	for _, fid := range feedIDs {
		if s, ok := m.DatafeedCounts[fid]; ok {
			res = append(res, s)
		}
	}
	return res, m.Err
}

func (m *MockXPack) StartDatafeed(ctx context.Context, feedID string, options *OpenDatafeedOptions) (bool, error) {
	return m.Ok, m.Err
}

func (m *MockXPack) StopDatafeed(ctx context.Context, feedID string, options *CloseDatafeedOptions) (bool, error) {
	return m.Ok, m.Err
}

func (m *MockXPack) GetJobs(ctx context.Context, jobIDs ...string) ([]JobSpec, error) {
	var res []JobSpec
	for _, fid := range jobIDs {
		if s, ok := m.Jobs[fid]; ok {
			res = append(res, s)
		}
	}
	return res, m.Err
}

func (m *MockXPack) GetJobStats(ctx context.Context, jobIDs ...string) ([]JobStatsSpec, error) {
	var res []JobStatsSpec
	for _, fid := range jobIDs {
		if s, ok := m.JobStats[fid]; ok {
			res = append(res, s)
		}
	}
	return res, m.Err
}

func (m *MockXPack) OpenJob(ctx context.Context, jobID string, options *OpenJobOptions) (bool, error) {
	return m.Ok, m.Err
}

func (m *MockXPack) CloseJob(ctx context.Context, jobID string, options *CloseJobOptions) (bool, error) {
	return m.Ok, m.Err
}

func (m *MockXPack) GetBuckets(ctx context.Context, jobID string, options *GetBucketsOptions) ([]BucketSpec, error) {
	return m.Buckets, m.Err
}

func (m *MockXPack) GetRecords(ctx context.Context, jobID string, options *GetRecordsOptions) ([]RecordSpec, error) {
	return m.Records, m.Err
}
