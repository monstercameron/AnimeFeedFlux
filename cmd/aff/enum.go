package main

import (
	"fmt"
	"strings"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// This file translates the CLI's plain-English flag values (--kind
// grounded) to and from the generated proto enums. Centralised here so
// every command spells a kind/origin/status the same way rather than each
// command file inventing its own strings.

func parseFeedKind(s string) (affv1.FeedKind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "generative":
		return affv1.FeedKind_FEED_KIND_GENERATIVE, nil
	case "grounded":
		return affv1.FeedKind_FEED_KIND_GROUNDED, nil
	case "aggregate":
		return affv1.FeedKind_FEED_KIND_AGGREGATE, nil
	default:
		return affv1.FeedKind_FEED_KIND_UNSPECIFIED,
			fmt.Errorf("aff: unknown --kind %q (want generative, grounded, or aggregate)", s)
	}
}

func parseOrigin(s string) (affv1.Origin, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return affv1.Origin_ORIGIN_UNSPECIFIED, nil
	case "generated":
		return affv1.Origin_ORIGIN_GENERATED, nil
	case "sampled":
		return affv1.Origin_ORIGIN_SAMPLED, nil
	case "manual":
		return affv1.Origin_ORIGIN_MANUAL, nil
	case "correction":
		return affv1.Origin_ORIGIN_CORRECTION, nil
	default:
		return affv1.Origin_ORIGIN_UNSPECIFIED,
			fmt.Errorf("aff: unknown --origin %q (want generated, sampled, manual, or correction)", s)
	}
}

func parseRunStatus(s string) (affv1.RunStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return affv1.RunStatus_RUN_STATUS_UNSPECIFIED, nil
	case "running":
		return affv1.RunStatus_RUN_STATUS_RUNNING, nil
	case "succeeded":
		return affv1.RunStatus_RUN_STATUS_SUCCEEDED, nil
	case "failed":
		return affv1.RunStatus_RUN_STATUS_FAILED, nil
	case "skipped":
		return affv1.RunStatus_RUN_STATUS_SKIPPED, nil
	default:
		return affv1.RunStatus_RUN_STATUS_UNSPECIFIED,
			fmt.Errorf("aff: unknown --status %q (want running, succeeded, failed, or skipped)", s)
	}
}

func parseDeletedFilter(s string) (affv1.DeletedFilter, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "exclude":
		return affv1.DeletedFilter_DELETED_FILTER_EXCLUDE_DELETED, nil
	case "only":
		return affv1.DeletedFilter_DELETED_FILTER_ONLY_DELETED, nil
	case "all":
		return affv1.DeletedFilter_DELETED_FILTER_INCLUDE_ALL, nil
	default:
		return affv1.DeletedFilter_DELETED_FILTER_UNSPECIFIED,
			fmt.Errorf("aff: unknown --deleted %q (want exclude, only, or all)", s)
	}
}
