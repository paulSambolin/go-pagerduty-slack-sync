package sync

import (
	"fmt"
	"strings"
	"time"

	"github.com/PagerDuty/go-pagerduty"
	"github.com/sirupsen/logrus"
)

const (
	pagerDutyMaxRateLimitRetries = 5
	pagerDutyInitialBackoff      = 5 * time.Second
	pagerDutyMaxBackoff          = 60 * time.Second
)

type pagerDutyClient struct {
	client *pagerduty.Client
}

func newPagerDutyClient(token string) *pagerDutyClient {
	return &pagerDutyClient{
		client: pagerduty.NewClient(token),
	}
}

func (p *pagerDutyClient) getEmailsForSchedule(ID string, lookahead time.Duration) ([]string, error) {
	var users []pagerduty.User
	err := retryPagerDutyOnRateLimit(fmt.Sprintf("ListOnCallUsers(%s)", ID), func() error {
		var err error
		users, err = p.client.ListOnCallUsers(ID, pagerduty.ListOnCallUsersOptions{
			Since: time.Now().UTC().Format(time.RFC3339),
			Until: time.Now().UTC().Add(lookahead).Format(time.RFC3339),
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	var results []string
	for _, user := range users {
		results = append(results, user.Email)
	}
	return results, nil
}

// retryPagerDutyOnRateLimit retries fn when the go-pagerduty client surfaces an
// HTTP 429. The library wraps API errors in a formatted string ("HTTP response
// code: 429"), so detection is done by substring match. Backoff is exponential
// from pagerDutyInitialBackoff up to pagerDutyMaxBackoff.
func retryPagerDutyOnRateLimit(op string, fn func() error) error {
	wait := pagerDutyInitialBackoff
	var err error
	for attempt := 0; attempt <= pagerDutyMaxRateLimitRetries; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}

		if !isPagerDutyRateLimitErr(err) {
			return err
		}

		if attempt == pagerDutyMaxRateLimitRetries {
			return fmt.Errorf("%s: gave up after %d retries: %w", op, pagerDutyMaxRateLimitRetries, err)
		}

		logrus.Warnf("%s: pagerduty rate limit hit, sleeping %s before retry %d/%d", op, wait, attempt+1, pagerDutyMaxRateLimitRetries)
		time.Sleep(wait)
		if wait *= 2; wait > pagerDutyMaxBackoff {
			wait = pagerDutyMaxBackoff
		}
	}
	return err
}

func isPagerDutyRateLimitErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "HTTP response code: 429")
}
