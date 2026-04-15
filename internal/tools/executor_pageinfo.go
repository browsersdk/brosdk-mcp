package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxEvaluateExpressionLen = 32 * 1024

type screenshotOptions struct {
	format   string
	fullPage bool
	quality  int
	hasPath  bool
	path     string
}

func (e *Executor) callEvaluate(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	expression, ok := getStringArg(args, "expression")
	if !ok || strings.TrimSpace(expression) == "" {
		return nil, fmt.Errorf("missing required argument expression")
	}
	expression, err := normalizeEvaluateExpression(expression)
	if err != nil {
		return nil, err
	}

	awaitPromise := true
	if v, ok, err := getBoolArg(args, "awaitPromise"); err != nil {
		return nil, err
	} else if ok {
		awaitPromise = v
	}

	returnByValue := true
	if v, ok, err := getBoolArg(args, "returnByValue"); err != nil {
		return nil, err
	} else if ok {
		returnByValue = v
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	raw, err := e.callPageClient(ctx, pageClient, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"awaitPromise":  awaitPromise,
		"returnByValue": returnByValue,
		"userGesture":   true,
	})
	if err != nil {
		return nil, fmt.Errorf("Runtime.evaluate failed: %w", err)
	}

	result, err := decodeRuntimeEvaluateResult(raw)
	if err != nil {
		return nil, err
	}

	resp := map[string]any{
		"ok":            true,
		"tabId":         currentTabID,
		"type":          result.Type,
		"awaitPromise":  awaitPromise,
		"returnByValue": returnByValue,
	}
	if result.Subtype != "" {
		resp["subtype"] = result.Subtype
	}
	if result.ClassName != "" {
		resp["className"] = result.ClassName
	}
	if result.Description != "" {
		resp["description"] = result.Description
	}
	if result.UnserializableValue != "" {
		resp["unserializableValue"] = result.UnserializableValue
	}
	if result.ObjectID != "" {
		resp["objectId"] = result.ObjectID
	}
	if returnByValue || result.Value != nil {
		resp["result"] = result.Value
	}

	return resp, nil
}

func (e *Executor) callScreenshot(ctx context.Context, args map[string]any) (map[string]any, error) {
	if tabID, ok := getStringArg(args, "tabId"); ok {
		if err := e.activateAndConnect(ctx, tabID); err != nil {
			return nil, err
		}
	}

	opts, err := resolveScreenshotOptions(args)
	if err != nil {
		return nil, err
	}

	params := map[string]any{
		"format":                opts.format,
		"captureBeyondViewport": opts.fullPage,
	}
	if opts.quality > 0 {
		params["quality"] = opts.quality
	}

	pageClient, currentTabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}

	raw, err := e.callPageClient(ctx, pageClient, "Page.captureScreenshot", params)
	if err != nil {
		return nil, fmt.Errorf("Page.captureScreenshot failed: %w", err)
	}

	data, decoded, err := decodeScreenshotData(raw)
	if err != nil {
		return nil, err
	}

	resp := map[string]any{
		"ok":       true,
		"tabId":    currentTabID,
		"format":   opts.format,
		"fullPage": opts.fullPage,
		"bytes":    len(decoded),
		"encoding": "base64",
		"saved":    opts.hasPath,
	}
	if opts.quality > 0 {
		resp["quality"] = opts.quality
	}

	if opts.hasPath {
		if err := os.MkdirAll(filepath.Dir(opts.path), 0o755); err != nil {
			return nil, fmt.Errorf("create screenshot directory: %w", err)
		}
		if err := os.WriteFile(opts.path, decoded, 0o644); err != nil {
			return nil, fmt.Errorf("write screenshot file: %w", err)
		}
		resp["path"] = opts.path
	} else {
		resp["data"] = data
	}

	return resp, nil
}

func normalizeEvaluateExpression(expression string) (string, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return "", fmt.Errorf("missing required argument expression")
	}
	if len(expression) > maxEvaluateExpressionLen {
		return "", fmt.Errorf("expression exceeds %d characters", maxEvaluateExpressionLen)
	}
	return expression, nil
}

func resolveScreenshotOptions(args map[string]any) (screenshotOptions, error) {
	opts := screenshotOptions{
		format: "png",
	}
	if v, ok := getStringArg(args, "format"); ok && strings.TrimSpace(v) != "" {
		opts.format = strings.ToLower(strings.TrimSpace(v))
	}
	switch opts.format {
	case "png", "jpeg", "webp":
	default:
		return screenshotOptions{}, fmt.Errorf("format must be one of png, jpeg, webp")
	}

	if v, ok, err := getBoolArg(args, "fullPage"); err != nil {
		return screenshotOptions{}, err
	} else if ok {
		opts.fullPage = v
	}

	if quality, ok, err := getIntArg(args, "quality"); err != nil {
		return screenshotOptions{}, err
	} else if ok {
		if opts.format == "png" {
			return screenshotOptions{}, fmt.Errorf("quality is only supported for jpeg or webp screenshots")
		}
		if quality < 1 || quality > 100 {
			return screenshotOptions{}, fmt.Errorf("quality must be between 1 and 100")
		}
		opts.quality = quality
	}

	if path, ok := getStringArg(args, "path"); ok && strings.TrimSpace(path) != "" {
		opts.hasPath = true
		opts.path = filepath.Clean(strings.TrimSpace(path))
	}

	return opts, nil
}
