package model

import (
	"context"
	"time"
)

// Query wrappers the channel health tests use to read seeded data back. The
// health page composes the same calls itself (LoadRouteHealthWindow, then
// Evidence, Percentiles, ChannelPercentiles or Buckets); these keep the tests'
// existing shape without shipping wrappers production never calls.

// QueryChannelHealthEvidence sums the window per (model, lane, channel, kind).
func QueryChannelHealthEvidence(since time.Time) ([]ChannelHealthEvidenceRow, error) {
	return QueryChannelHealthEvidenceContext(context.Background(), since)
}

func QueryChannelHealthEvidenceContext(ctx context.Context, since time.Time) ([]ChannelHealthEvidenceRow, error) {
	window, err := LoadRouteHealthWindow(ctx, since, time.Now())
	if err != nil {
		return nil, err
	}
	return window.Evidence(), nil
}

// QueryChannelHealthPercentiles ranks each lane's latency figures over the
// routes serving it now, leaving out excluded routes.
func QueryChannelHealthPercentiles(since time.Time, excluded ...ChannelHealthRouteRef) ([]ChannelHealthPercentileRow, error) {
	return QueryChannelHealthPercentilesWithAliases(since, nil, excluded...)
}

// QueryChannelHealthPercentilesWithAliases ranks routed variants together
// under their published catalog name.
func QueryChannelHealthPercentilesWithAliases(since time.Time, aliases map[string]string, excluded ...ChannelHealthRouteRef) ([]ChannelHealthPercentileRow, error) {
	return QueryChannelHealthPercentilesWithAliasesContext(context.Background(), since, aliases, excluded...)
}

func QueryChannelHealthPercentilesWithAliasesContext(ctx context.Context, since time.Time, aliases map[string]string, excluded ...ChannelHealthRouteRef) ([]ChannelHealthPercentileRow, error) {
	return queryRouteHealthPercentiles(ctx, since, RouteHealthPopulation{Aliases: aliases, Excluded: excluded})
}

// QueryChannelHealthPublicPercentilesWithAliases pools measurements across
// routes that share a public lane, for the named catalog models.
func QueryChannelHealthPublicPercentilesWithAliases(since time.Time, aliases map[string]string, models []string, excluded ...ChannelHealthRouteRef) ([]ChannelHealthPercentileRow, error) {
	return QueryChannelHealthPublicPercentilesWithAliasesContext(context.Background(), since, aliases, models, excluded...)
}

func QueryChannelHealthPublicPercentilesWithAliasesContext(ctx context.Context, since time.Time, aliases map[string]string, models []string, excluded ...ChannelHealthRouteRef) ([]ChannelHealthPercentileRow, error) {
	if len(models) == 0 {
		return []ChannelHealthPercentileRow{}, ctx.Err()
	}
	return queryRouteHealthPercentiles(ctx, since, RouteHealthPopulation{Aliases: aliases, PublicModels: models, Excluded: excluded})
}

func queryRouteHealthPercentiles(ctx context.Context, since time.Time, population RouteHealthPopulation) ([]ChannelHealthPercentileRow, error) {
	grid, err := LoadChannelHealthGridContext(ctx)
	if err != nil {
		return nil, err
	}
	window, err := LoadRouteHealthWindow(ctx, since, time.Now())
	if err != nil {
		return nil, err
	}
	population.Current = RouteHealthCurrentCells(grid)
	return window.Percentiles(population), nil
}

// QueryChannelHealthChannelPercentiles ranks each upstream's own total
// duration across every lane it serves, over customer requests.
func QueryChannelHealthChannelPercentiles(since time.Time) ([]ChannelHealthChannelPercentileRow, error) {
	return QueryChannelHealthChannelPercentilesContext(context.Background(), since)
}

func QueryChannelHealthChannelPercentilesContext(ctx context.Context, since time.Time) ([]ChannelHealthChannelPercentileRow, error) {
	grid, err := LoadChannelHealthGridContext(ctx)
	if err != nil {
		return nil, err
	}
	window, err := LoadRouteHealthWindow(ctx, since, time.Now())
	if err != nil {
		return nil, err
	}
	return window.ChannelPercentiles(RouteHealthEnabledChannels(grid)), nil
}

// QueryChannelHealthBuckets slices the window into bucketCount slices of
// bucketSeconds per (model, lane, channel, kind), customer answers and probes
// only. It powers the heat wall.
func QueryChannelHealthBuckets(since time.Time, bucketSeconds int64, bucketCount int) ([]ChannelHealthBucketRow, error) {
	return QueryChannelHealthBucketsContext(context.Background(), since, bucketSeconds, bucketCount)
}

func QueryChannelHealthBucketsContext(ctx context.Context, since time.Time, bucketSeconds int64, bucketCount int) ([]ChannelHealthBucketRow, error) {
	if bucketSeconds <= 0 || bucketCount <= 0 {
		return []ChannelHealthBucketRow{}, ctx.Err()
	}
	until := since.Add(time.Duration(bucketSeconds*int64(bucketCount)) * time.Second)
	window, err := LoadRouteHealthWindow(ctx, since, until)
	if err != nil {
		return nil, err
	}
	return window.Buckets(since, bucketSeconds, bucketCount, false), nil
}
