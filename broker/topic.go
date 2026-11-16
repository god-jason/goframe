package broker

import (
	"strings"
	"unicode/utf8"
)

// validateTopicName 校验发布主题：非空、合法 UTF-8、不含通配符和空字符。
func validateTopicName(topic string) error {
	if topic == "" || len(topic) > 65535 || !utf8.ValidString(topic) || containsNull(topic) {
		return ErrMalformedPacket
	}
	if strings.ContainsAny(topic, "+#") {
		return ErrMalformedPacket
	}
	return nil
}

// validateFilter 校验订阅过滤器。
func validateFilter(filter string) error {
	if filter == "" || len(filter) > 65535 || !utf8.ValidString(filter) || containsNull(filter) {
		return ErrMalformedPacket
	}
	levels := strings.Split(filter, "/")
	for i, level := range levels {
		if strings.Contains(level, "#") {
			if level != "#" || i != len(levels)-1 {
				return ErrMalformedPacket
			}
		}
		if strings.Contains(level, "+") && level != "+" {
			return ErrMalformedPacket
		}
	}
	return nil
}

// topicMatches 判断主题是否匹配过滤器。
// 以 $ 开头的系统主题不会被首级通配符匹配。
func topicMatches(filter, topic string) bool {
	if strings.HasPrefix(topic, "$") {
		first, _, _ := strings.Cut(filter, "/")
		if first == "#" || first == "+" {
			return false
		}
	}
	filters := strings.Split(filter, "/")
	topics := strings.Split(topic, "/")
	for i, level := range filters {
		if level == "#" {
			return true
		}
		if i >= len(topics) {
			return false
		}
		if level != "+" && level != topics[i] {
			return false
		}
	}
	return len(filters) == len(topics)
}

func containsNull(value string) bool {
	return strings.IndexByte(value, 0) >= 0
}
