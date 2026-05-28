package sync

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

const (
	defaultMaxRateLimitRetries = 5
	defaultRateLimitWait       = 30 * time.Second
)

type slackClient struct {
	users      []slack.User
	userGroups []slack.UserGroup
	Client     *slack.Client
}

func newSlackClient(token string) (*slackClient, error) {
	s := slack.New(token)

	var userGroups []slack.UserGroup
	if err := retryOnRateLimit("GetUserGroups", func() error {
		var err error
		userGroups, err = s.GetUserGroups()
		return err
	}); err != nil {
		return nil, err
	}

	var users []slack.User
	if err := retryOnRateLimit("GetUsers", func() error {
		var err error
		users, err = s.GetUsers()
		return err
	}); err != nil {
		return nil, err
	}

	return &slackClient{
		users:      users,
		userGroups: userGroups,
		Client:     s,
	}, nil
}

func (s *slackClient) createOrGetUserGroup(name string) (*slack.UserGroup, error) {
	group := s.findUserGroupByName(name)
	if group != nil {
		return group, nil
	}

	var g slack.UserGroup
	if err := retryOnRateLimit("CreateUserGroup", func() error {
		var err error
		g, err = s.Client.CreateUserGroup(slack.UserGroup{
			Name:   name,
			Handle: name,
		})
		return err
	}); err != nil {
		return nil, err
	}

	return &g, nil
}

func (s *slackClient) getUserGroupMembers(userGroupID string) ([]string, error) {
	var members []string
	err := retryOnRateLimit("GetUserGroupMembers", func() error {
		var err error
		members, err = s.Client.GetUserGroupMembers(userGroupID)
		return err
	})
	return members, err
}

func (s *slackClient) updateUserGroupMembers(userGroupID, members string) error {
	return retryOnRateLimit("UpdateUserGroupMembers", func() error {
		_, err := s.Client.UpdateUserGroupMembers(userGroupID, members)
		return err
	})
}

func (s *slackClient) getSlackIDsFromEmails(emails []string) ([]string, error) {
	var results []string
	for _, email := range emails {
		ID := s.findUserIDByEmail(email)
		if ID == nil {
			return nil, fmt.Errorf("could not find slack user with email: %s", email)
		}
		results = append(results, *ID)
	}
	return results, nil
}

func (s *slackClient) findUserIDByEmail(email string) *string {
	for _, u := range s.users {
		if strings.EqualFold(email, u.Profile.Email) {
			return &u.ID
		}
	}
	return nil
}

func (s *slackClient) findUserGroupByName(name string) *slack.UserGroup {
	for _, g := range s.userGroups {
		if strings.EqualFold(name, g.Name) {
			return &g
		}
	}
	return nil
}

// retryOnRateLimit retries fn when Slack returns *slack.RateLimitedError,
// honoring the Retry-After hint. Non-rate-limit errors are returned immediately.
func retryOnRateLimit(op string, fn func() error) error {
	var err error
	for attempt := 0; attempt <= defaultMaxRateLimitRetries; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}

		var rl *slack.RateLimitedError
		if !errors.As(err, &rl) {
			return err
		}

		if attempt == defaultMaxRateLimitRetries {
			return fmt.Errorf("%s: gave up after %d retries: %w", op, defaultMaxRateLimitRetries, err)
		}

		wait := rl.RetryAfter
		if wait <= 0 {
			wait = defaultRateLimitWait
		}
		logrus.Warnf("%s: slack rate limit hit, sleeping %s before retry %d/%d", op, wait, attempt+1, defaultMaxRateLimitRetries)
		time.Sleep(wait)
	}
	return err
}
