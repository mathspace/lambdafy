package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

func handleCron(ctx context.Context, cronName string) error {
	u := fmt.Sprintf("http://%s/_lambdafy/cron?name=%s", appEndpoint, url.QueryEscape(cronName))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return fmt.Errorf("error creating HTTP request for cron '%s': %v", cronName, err)
	}
	req.Header.Add("Content-Length", "0")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending HTTP request for cron '%s': %v", cronName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("error sending HTTP request for cron '%s': %v", cronName, resp.Status)
	}
	return nil
}
