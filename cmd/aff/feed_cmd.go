package main

import (
	"context"
	"flag"
	"fmt"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

func (a *app) cmdFeed(args []string) int {
	choices := []string{"list", "get", "create", "update", "enable", "disable", "delete"}
	if len(args) == 0 {
		return a.missingSubcommand("feed", choices...)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return a.cmdFeedList(rest)
	case "get":
		return a.cmdFeedGet(rest)
	case "create":
		return a.cmdFeedCreate(rest)
	case "update":
		return a.cmdFeedUpdate(rest)
	case "enable":
		return a.cmdFeedSetEnabled(rest, true)
	case "disable":
		return a.cmdFeedSetEnabled(rest, false)
	case "delete":
		return a.cmdFeedDelete(rest)
	default:
		return a.unknownSubcommand("feed", sub, choices...)
	}
}

func (a *app) cmdFeedList(args []string) int {
	fs := a.newFlagSet("aff feed list")
	search := fs.String("search", "", "filter by title/slug substring")
	enabledOnly := fs.Bool("enabled-only", false, "only show enabled feeds")
	pageSize := fs.Int("page-size", 0, "page size (0 = server default)")
	pageToken := fs.String("page-token", "", "opaque pagination cursor from a previous call")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff feed list: %v\n", err)
		return exitFail
	}

	resp, err := cb.Feed.List(ctx, &affv1.FeedServiceListRequest{
		Search:      *search,
		EnabledOnly: *enabledOnly,
		PageSize:    int32(*pageSize),
		PageToken:   *pageToken,
	})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff feed list: %v\n", err)
		return exitFail
	}

	if a.JSON {
		if err := a.printProtoJSON(resp); err != nil {
			fmt.Fprintln(a.Stderr, err)
			return exitFail
		}
		return exitOK
	}

	tw := newTabWriter(a.Stdout)
	fmt.Fprintln(tw, "ID\tSLUG\tKIND\tENABLED\tTITLE")
	for _, f := range resp.GetFeeds() {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%t\t%s\n", f.GetId(), f.GetSlug(), f.GetKind(), f.GetEnabled(), f.GetTitle())
	}
	_ = tw.Flush()
	if resp.GetNextPageToken() != "" {
		fmt.Fprintf(a.Stdout, "next-page-token: %s\n", resp.GetNextPageToken())
	}
	return exitOK
}

func (a *app) cmdFeedGet(args []string) int {
	fs := a.newFlagSet("aff feed get")
	id := fs.Int64("id", 0, "feed id")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	slug := ""
	if fs.NArg() > 0 {
		slug = fs.Arg(0)
	}
	if *id == 0 && slug == "" {
		fmt.Fprintln(a.Stderr, "aff feed get: need a slug argument or --id")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff feed get: %v\n", err)
		return exitFail
	}

	resp, err := cb.Feed.Get(ctx, &affv1.FeedServiceGetRequest{Id: *id, Slug: slug})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff feed get: %v\n", err)
		return exitFail
	}
	return a.printFeed(resp.GetFeed())
}

func (a *app) printFeed(f *affv1.Feed) int {
	if a.JSON {
		if err := a.printProtoJSON(f); err != nil {
			fmt.Fprintln(a.Stderr, err)
			return exitFail
		}
		return exitOK
	}
	fmt.Fprintf(a.Stdout, "id:       %d\n", f.GetId())
	fmt.Fprintf(a.Stdout, "slug:     %s\n", f.GetSlug())
	fmt.Fprintf(a.Stdout, "kind:     %s\n", f.GetKind())
	fmt.Fprintf(a.Stdout, "title:    %s\n", f.GetTitle())
	fmt.Fprintf(a.Stdout, "enabled:  %t\n", f.GetEnabled())
	fmt.Fprintf(a.Stdout, "version:  %d\n", f.GetVersion())
	return exitOK
}

// feedScalarFlags binds the flags shared by `feed create` and `feed update`
// that map directly to top-level Feed fields (not the nested FeedSpec,
// which comes from --spec-json/--spec-file — see specjson.go).
type feedScalarFlags struct {
	slug        *string
	kind        *string
	title       *string
	description *string
	language    *string
	author      *string
	copyright   *string
	ogImage     *string
	ttlMinutes  *int
	specJSON    *string
	specFile    *string
}

func bindFeedScalarFlags(fs *flag.FlagSet) feedScalarFlags {
	return feedScalarFlags{
		slug:        fs.String("slug", "", "feed slug (immutable after first publish)"),
		kind:        fs.String("kind", "generative", "generative, grounded, or aggregate"),
		title:       fs.String("title", "", "feed title"),
		description: fs.String("description", "", "feed description"),
		language:    fs.String("language", "en-us", "feed language tag"),
		author:      fs.String("author", "", "feed author"),
		copyright:   fs.String("copyright", "", "feed copyright line"),
		ogImage:     fs.String("og-image", "", "og:image URL"),
		ttlMinutes:  fs.Int("ttl-minutes", 60, "channel TTL in minutes"),
		specJSON:    fs.String("spec-json", "", "FeedSpec as inline JSON (see proto/aff/v1/feed.proto)"),
		specFile:    fs.String("spec-file", "", "FeedSpec as a JSON file"),
	}
}

func (a *app) cmdFeedCreate(args []string) int {
	fs := a.newFlagSet("aff feed create")
	sf := bindFeedScalarFlags(fs)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	kind, err := parseFeedKind(*sf.kind)
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitUsage
	}
	if *sf.slug == "" {
		fmt.Fprintln(a.Stderr, "aff feed create: --slug is required")
		return exitUsage
	}

	spec := &affv1.FeedSpec{}
	if err := loadProtoJSON(*sf.specJSON, *sf.specFile, spec); err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitUsage
	}

	feed := &affv1.Feed{
		Slug:        *sf.slug,
		Kind:        kind,
		Title:       *sf.title,
		Description: *sf.description,
		Language:    *sf.language,
		Author:      *sf.author,
		Copyright:   *sf.copyright,
		OgImage:     *sf.ogImage,
		TtlMinutes:  int32(*sf.ttlMinutes),
	}
	if kind != affv1.FeedKind_FEED_KIND_AGGREGATE {
		feed.Spec = spec
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff feed create: %v\n", err)
		return exitFail
	}
	resp, err := cb.Feed.Create(ctx, &affv1.FeedServiceCreateRequest{Feed: feed})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff feed create: %v\n", err)
		return exitFail
	}
	return a.printFeed(resp.GetFeed())
}

func (a *app) cmdFeedUpdate(args []string) int {
	fs := a.newFlagSet("aff feed update")
	id := fs.Int64("id", 0, "feed id (required)")
	expectedVersion := fs.Int64("expected-version", 0, "the version last read, for optimistic concurrency (required)")
	sf := bindFeedScalarFlags(fs)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *id == 0 {
		fmt.Fprintln(a.Stderr, "aff feed update: --id is required")
		return exitUsage
	}
	if *expectedVersion == 0 {
		fmt.Fprintln(a.Stderr, "aff feed update: --expected-version is required (PLAN.md §11 optimistic concurrency)")
		return exitUsage
	}
	kind, err := parseFeedKind(*sf.kind)
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitUsage
	}

	spec := &affv1.FeedSpec{}
	if err := loadProtoJSON(*sf.specJSON, *sf.specFile, spec); err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitUsage
	}

	feed := &affv1.Feed{
		Id:          *id,
		Slug:        *sf.slug,
		Kind:        kind,
		Title:       *sf.title,
		Description: *sf.description,
		Language:    *sf.language,
		Author:      *sf.author,
		Copyright:   *sf.copyright,
		OgImage:     *sf.ogImage,
		TtlMinutes:  int32(*sf.ttlMinutes),
	}
	if kind != affv1.FeedKind_FEED_KIND_AGGREGATE {
		feed.Spec = spec
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff feed update: %v\n", err)
		return exitFail
	}
	resp, err := cb.Feed.Update(ctx, &affv1.FeedServiceUpdateRequest{Feed: feed, ExpectedVersion: *expectedVersion})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff feed update: %v\n", err)
		return exitFail
	}
	return a.printFeed(resp.GetFeed())
}

func (a *app) cmdFeedSetEnabled(args []string, enabled bool) int {
	name := "aff feed disable"
	if enabled {
		name = "aff feed enable"
	}
	fs := a.newFlagSet(name)
	expectedVersion := fs.Int64("expected-version", 0, "the version last read, for optimistic concurrency (required)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(a.Stderr, "%s: want exactly one feed id argument\n", name)
		return exitUsage
	}
	id, err := parseInt64Arg(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(a.Stderr, "%s: %v\n", name, err)
		return exitUsage
	}
	if *expectedVersion == 0 {
		fmt.Fprintf(a.Stderr, "%s: --expected-version is required\n", name)
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "%s: %v\n", name, err)
		return exitFail
	}
	resp, err := cb.Feed.SetEnabled(ctx, &affv1.FeedServiceSetEnabledRequest{
		FeedId: id, Enabled: enabled, ExpectedVersion: *expectedVersion,
	})
	if err != nil {
		fmt.Fprintf(a.Stderr, "%s: %v\n", name, err)
		return exitFail
	}
	return a.printFeed(resp.GetFeed())
}

func (a *app) cmdFeedDelete(args []string) int {
	fs := a.newFlagSet("aff feed delete")
	expectedVersion := fs.Int64("expected-version", 0, "the version last read, for optimistic concurrency (required)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Stderr, "aff feed delete: want exactly one feed id argument")
		return exitUsage
	}
	id, err := parseInt64Arg(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff feed delete: %v\n", err)
		return exitUsage
	}
	if *expectedVersion == 0 {
		fmt.Fprintln(a.Stderr, "aff feed delete: --expected-version is required")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff feed delete: %v\n", err)
		return exitFail
	}
	if _, err := cb.Feed.Delete(ctx, &affv1.FeedServiceDeleteRequest{FeedId: id, ExpectedVersion: *expectedVersion}); err != nil {
		fmt.Fprintf(a.Stderr, "aff feed delete: %v\n", err)
		return exitFail
	}
	if a.JSON {
		_ = a.printJSON(map[string]any{"deleted": true, "feed_id": id})
	} else {
		fmt.Fprintf(a.Stdout, "Feed %d deleted.\n", id)
	}
	return exitOK
}
