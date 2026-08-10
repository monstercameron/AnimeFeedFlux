package main

import (
	"context"
	"fmt"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

func (a *app) cmdItem(args []string) int {
	choices := []string{"list", "get", "create", "update", "delete", "restore", "correct"}
	if len(args) == 0 {
		return a.missingSubcommand("item", choices...)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return a.cmdItemList(rest)
	case "get":
		return a.cmdItemGet(rest)
	case "create":
		return a.cmdItemCreate(rest)
	case "update":
		return a.cmdItemUpdate(rest)
	case "delete":
		return a.cmdItemDelete(rest)
	case "restore":
		return a.cmdItemRestore(rest)
	case "correct":
		return a.cmdItemCorrect(rest)
	default:
		return a.unknownSubcommand("item", sub, choices...)
	}
}

func (a *app) printItem(it *affv1.Item) int {
	if a.JSON {
		if err := a.printProtoJSON(it); err != nil {
			fmt.Fprintln(a.Stderr, err)
			return exitFail
		}
		return exitOK
	}
	fmt.Fprintf(a.Stdout, "id:          %d\n", it.GetId())
	fmt.Fprintf(a.Stdout, "feed_id:     %d\n", it.GetFeedId())
	fmt.Fprintf(a.Stdout, "item_key:    %s\n", it.GetItemKey())
	fmt.Fprintf(a.Stdout, "title:       %s\n", it.GetTitle())
	fmt.Fprintf(a.Stdout, "origin:      %s\n", it.GetOrigin())
	fmt.Fprintf(a.Stdout, "link:        %s\n", it.GetLink())
	fmt.Fprintf(a.Stdout, "version:     %d\n", it.GetVersion())
	return exitOK
}

func (a *app) cmdItemList(args []string) int {
	fs := a.newFlagSet("aff item list")
	query := fs.String("query", "", "FTS5 search over title/summary/body")
	feedID := fs.Int64("feed", 0, "filter by feed id")
	origin := fs.String("origin", "", "generated, sampled, manual, or correction")
	deleted := fs.String("deleted", "exclude", "exclude, only, or all")
	pageSize := fs.Int("page-size", 0, "page size (0 = server default)")
	pageToken := fs.String("page-token", "", "opaque pagination cursor")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	originVal, err := parseOrigin(*origin)
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitUsage
	}
	deletedVal, err := parseDeletedFilter(*deleted)
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item list: %v\n", err)
		return exitFail
	}
	resp, err := cb.Item.List(ctx, &affv1.ItemServiceListRequest{
		PageSize:      int32(*pageSize),
		PageToken:     *pageToken,
		Query:         *query,
		FeedId:        *feedID,
		Origin:        originVal,
		DeletedFilter: deletedVal,
	})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item list: %v\n", err)
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
	fmt.Fprintln(tw, "ID\tFEED\tORIGIN\tTITLE")
	for _, it := range resp.GetItems() {
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\n", it.GetId(), it.GetFeedId(), it.GetOrigin(), it.GetTitle())
	}
	_ = tw.Flush()
	if resp.GetNextPageToken() != "" {
		fmt.Fprintf(a.Stdout, "next-page-token: %s\n", resp.GetNextPageToken())
	}
	return exitOK
}

func (a *app) cmdItemGet(args []string) int {
	fs := a.newFlagSet("aff item get")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Stderr, "aff item get: want exactly one item id argument")
		return exitUsage
	}
	id, err := parseInt64Arg(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item get: %v\n", err)
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item get: %v\n", err)
		return exitFail
	}
	resp, err := cb.Item.Get(ctx, &affv1.ItemServiceGetRequest{Id: id})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item get: %v\n", err)
		return exitFail
	}
	return a.printItem(resp.GetItem())
}

func (a *app) cmdItemCreate(args []string) int {
	fs := a.newFlagSet("aff item create")
	feedID := fs.Int64("feed", 0, "feed id (required)")
	title := fs.String("title", "", "item title (required)")
	summary := fs.String("summary", "", "plain-text summary")
	bodyHTML := fs.String("body", "", "full HTML body")
	link := fs.String("link", "", "canonical link")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *feedID == 0 || *title == "" {
		fmt.Fprintln(a.Stderr, "aff item create: --feed and --title are required")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item create: %v\n", err)
		return exitFail
	}
	resp, err := cb.Item.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId:      *feedID,
		Title:       *title,
		SummaryText: *summary,
		BodyHtml:    *bodyHTML,
		Link:        *link,
	}})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item create: %v\n", err)
		return exitFail
	}
	return a.printItem(resp.GetItem())
}

func (a *app) cmdItemUpdate(args []string) int {
	fs := a.newFlagSet("aff item update")
	id := fs.Int64("id", 0, "item id (required)")
	expectedVersion := fs.Int64("expected-version", 0, "the version last read (required)")
	title := fs.String("title", "", "new title")
	summary := fs.String("summary", "", "new plain-text summary")
	bodyHTML := fs.String("body", "", "new full HTML body")
	link := fs.String("link", "", "new canonical link")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *id == 0 || *expectedVersion == 0 {
		fmt.Fprintln(a.Stderr, "aff item update: --id and --expected-version are required")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item update: %v\n", err)
		return exitFail
	}
	resp, err := cb.Item.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id:          *id,
			Title:       *title,
			SummaryText: *summary,
			BodyHtml:    *bodyHTML,
			Link:        *link,
		},
		ExpectedVersion: *expectedVersion,
	})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item update: %v\n", err)
		return exitFail
	}
	return a.printItem(resp.GetItem())
}

func (a *app) cmdItemDelete(args []string) int {
	fs := a.newFlagSet("aff item delete")
	expectedVersion := fs.Int64("expected-version", 0, "the version last read (required)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Stderr, "aff item delete: want exactly one item id argument")
		return exitUsage
	}
	id, err := parseInt64Arg(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item delete: %v\n", err)
		return exitUsage
	}
	if *expectedVersion == 0 {
		fmt.Fprintln(a.Stderr, "aff item delete: --expected-version is required")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item delete: %v\n", err)
		return exitFail
	}
	if _, err := cb.Item.Delete(ctx, &affv1.ItemServiceDeleteRequest{ItemId: id, ExpectedVersion: *expectedVersion}); err != nil {
		fmt.Fprintf(a.Stderr, "aff item delete: %v\n", err)
		return exitFail
	}
	if a.JSON {
		_ = a.printJSON(map[string]any{"deleted": true, "item_id": id})
	} else {
		// This is a soft delete: the permalink now 410s, the guid is never
		// reused (PLAN.md §12.4) — worth saying so, since "deleted" alone
		// invites the wrong mental model of a hard delete.
		fmt.Fprintf(a.Stdout, "Item %d soft-deleted (permalink now 410s; guid never reused).\n", id)
	}
	return exitOK
}

func (a *app) cmdItemRestore(args []string) int {
	fs := a.newFlagSet("aff item restore")
	expectedVersion := fs.Int64("expected-version", 0, "the version last read (required)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Stderr, "aff item restore: want exactly one item id argument")
		return exitUsage
	}
	id, err := parseInt64Arg(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item restore: %v\n", err)
		return exitUsage
	}
	if *expectedVersion == 0 {
		fmt.Fprintln(a.Stderr, "aff item restore: --expected-version is required")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item restore: %v\n", err)
		return exitFail
	}
	resp, err := cb.Item.Restore(ctx, &affv1.ItemServiceRestoreRequest{ItemId: id, ExpectedVersion: *expectedVersion})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item restore: %v\n", err)
		return exitFail
	}
	return a.printItem(resp.GetItem())
}

// cmdItemCorrect implements `aff item correct`: ItemService.PublishCorrection,
// the only mechanism that reaches subscribers after the fact (PLAN.md §12.4:
// "RSS has no retraction"). It creates a NEW item, prefixed "Correction:", and
// does not touch the original row.
func (a *app) cmdItemCorrect(args []string) int {
	fs := a.newFlagSet("aff item correct")
	title := fs.String("title", "", "correction title (required)")
	summary := fs.String("summary", "", "correction plain-text summary (required)")
	bodyHTML := fs.String("body", "", "correction full HTML body")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Stderr, "aff item correct: want exactly one item id argument (the item being corrected)")
		return exitUsage
	}
	id, err := parseInt64Arg(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item correct: %v\n", err)
		return exitUsage
	}
	if *title == "" || *summary == "" {
		fmt.Fprintln(a.Stderr, "aff item correct: --title and --summary are required")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item correct: %v\n", err)
		return exitFail
	}
	resp, err := cb.Item.PublishCorrection(ctx, &affv1.ItemServicePublishCorrectionRequest{
		CorrectsItemId: id, Title: *title, SummaryText: *summary, BodyHtml: *bodyHTML,
	})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff item correct: %v\n", err)
		return exitFail
	}
	return a.printItem(resp.GetItem())
}
