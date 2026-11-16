package broker

import "testing"

func TestTopicMatches(t *testing.T) {
	cases := []struct {
		filter string
		topic  string
		want   bool
	}{
		{"sport/tennis/player1", "sport/tennis/player1", true},
		{"sport/tennis/+", "sport/tennis/player1", true},
		{"sport/tennis/+", "sport/tennis/player1/ranking", false},
		{"sport/#", "sport", true},
		{"sport/#", "sport/tennis/player1", true},
		{"#", "any/topic", true},
		{"+/monitor", "/monitor", true},
		{"+", "$SYS/uptime", false},
		{"#", "$SYS/uptime", false},
		{"$SYS/#", "$SYS/uptime", true},
		{"a/+/c", "a/b/c", true},
		{"a/+/c", "a/b/d", false},
	}
	for _, tc := range cases {
		if got := topicMatches(tc.filter, tc.topic); got != tc.want {
			t.Fatalf("topicMatches(%q, %q) = %v, want %v", tc.filter, tc.topic, got, tc.want)
		}
	}
}

func TestValidateFilterAndTopic(t *testing.T) {
	validFilters := []string{"#", "+", "a/b", "a/+/c", "a/#", "+/+"}
	for _, filter := range validFilters {
		if err := validateFilter(filter); err != nil {
			t.Fatalf("validateFilter(%q): %v", filter, err)
		}
	}

	invalidFilters := []string{"", "a/#/b", "a#", "a/b+", "a/++"}
	for _, filter := range invalidFilters {
		if err := validateFilter(filter); err == nil {
			t.Fatalf("validateFilter(%q) should fail", filter)
		}
	}

	validTopics := []string{"a", "a/b", "/a", "a//b", "$SYS/uptime"}
	for _, topic := range validTopics {
		if err := validateTopicName(topic); err != nil {
			t.Fatalf("validateTopicName(%q): %v", topic, err)
		}
	}
	invalidTopics := []string{"", "a/+", "a/#", "a\x00b", string([]byte{0xff})}
	for _, topic := range invalidTopics {
		if err := validateTopicName(topic); err == nil {
			t.Fatalf("validateTopicName(%q) should fail", topic)
		}
	}
}
