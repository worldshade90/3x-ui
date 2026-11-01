package speedlimit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const tagPrefix = "xui-sl-"

// Inject prepends per-client rules and marked outbound clones.
// An outbound that already sets sockopt.mark is not cloned, so policy routing keeps its mark.
func Inject(routingRaw, outboundRaw []byte, clients []Client) ([]byte, []byte, error) {
	active := usable(clients)
	if len(active) == 0 {
		return routingRaw, outboundRaw, nil
	}
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })

	var outs []map[string]any
	if err := json.Unmarshal(outboundRaw, &outs); err != nil {
		return nil, nil, fmt.Errorf("speed limit: outbounds: %w", err)
	}
	routing := map[string]any{}
	trimmed := bytes.TrimSpace(routingRaw)
	if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if err := json.Unmarshal(trimmed, &routing); err != nil {
			return nil, nil, fmt.Errorf("speed limit: routing: %w", err)
		}
	}
	rules := asRules(routing["rules"])
	defaultTag := ""
	byTag := make(map[string]map[string]any, len(outs))
	for _, ob := range outs {
		tag, _ := ob["tag"].(string)
		if tag == "" {
			continue
		}
		byTag[tag] = ob
		if defaultTag == "" {
			defaultTag = tag
		}
	}
	if defaultTag == "" {
		return nil, nil, fmt.Errorf("speed limit: no tagged outbound")
	}

	type cloneKey struct {
		id  int
		tag string
	}
	clones := map[cloneKey]string{}
	var added []map[string]any
	clone := func(id int, tag string, mark uint32) string {
		key := cloneKey{id: id, tag: tag}
		if existing, ok := clones[key]; ok {
			return existing
		}
		ob := byTag[tag]
		copied, ok := cloneWithMark(ob, fmt.Sprintf("%s%d-%s", tagPrefix, id, tag), mark)
		if !ok {
			clones[key] = tag
			return tag
		}
		newTag, _ := copied["tag"].(string)
		clones[key] = newTag
		added = append(added, copied)
		return newTag
	}

	prepend := make([]any, 0, len(active)*(len(rules)+1))
	for _, client := range active {
		mark, _ := Mark(client.ID)
		for _, rule := range rules {
			if !ruleAllows(rule, client.Email) {
				continue
			}
			cp := deepCopyMap(rule)
			cp["user"] = []any{client.Email}
			if tag, ok := cp["outboundTag"].(string); ok && tag != "" {
				cp["outboundTag"] = clone(client.ID, tag, mark)
			}
			prepend = append(prepend, cp)
		}
		prepend = append(prepend, map[string]any{
			"type":        "field",
			"user":        []any{client.Email},
			"outboundTag": clone(client.ID, defaultTag, mark),
		})
	}
	combined := make([]any, 0, len(prepend)+len(rules))
	combined = append(combined, prepend...)
	for _, rule := range rules {
		combined = append(combined, rule)
	}
	routing["rules"] = combined

	newRouting, err := json.Marshal(routing)
	if err != nil {
		return nil, nil, err
	}
	newOuts := append(outs, added...)
	newOutbound, err := json.Marshal(newOuts)
	if err != nil {
		return nil, nil, err
	}
	return newRouting, newOutbound, nil
}

// Fingerprint identifies the speed-limit slice of a running Xray config so a
// later edit can be detected without comparing the whole template.
func Fingerprint(routingRaw, outboundRaw []byte) string {
	var outs []struct {
		Tag string `json:"tag"`
	}
	_ = json.Unmarshal(outboundRaw, &outs)
	tags := make([]string, 0)
	for _, ob := range outs {
		if strings.HasPrefix(ob.Tag, tagPrefix) {
			tags = append(tags, ob.Tag)
		}
	}
	var routing struct {
		Rules []map[string]any `json:"rules"`
	}
	_ = json.Unmarshal(routingRaw, &routing)
	rules := make([]string, 0)
	for _, rule := range routing.Rules {
		tag, _ := rule["outboundTag"].(string)
		if !strings.HasPrefix(tag, tagPrefix) {
			continue
		}
		rules = append(rules, fmt.Sprint(rule["user"])+"->"+tag)
	}
	sort.Strings(tags)
	sort.Strings(rules)
	if len(tags) == 0 && len(rules) == 0 {
		return ""
	}
	return strings.Join(tags, ",") + "|" + strings.Join(rules, ";")
}

func cloneWithMark(ob map[string]any, tag string, mark uint32) (map[string]any, bool) {
	if ob == nil || !dialCloneable(ob) {
		return nil, false
	}
	cp := deepCopyMap(ob)
	ss, _ := cp["streamSettings"].(map[string]any)
	if ss == nil {
		ss = map[string]any{}
		cp["streamSettings"] = ss
	}
	so, _ := ss["sockopt"].(map[string]any)
	if so == nil {
		so = map[string]any{}
		ss["sockopt"] = so
	}
	if existingMark(so["mark"]) != 0 {
		return nil, false
	}
	so["mark"] = int(mark)
	cp["tag"] = tag
	return cp, true
}

func dialCloneable(ob map[string]any) bool {
	protocol, _ := ob["protocol"].(string)
	switch strings.ToLower(protocol) {
	case "freedom", "vless", "vmess", "trojan", "shadowsocks", "socks", "http", "hysteria", "wireguard":
		return true
	default:
		return false
	}
}

func existingMark(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func ruleAllows(rule map[string]any, email string) bool {
	raw, ok := rule["user"]
	if !ok || raw == nil {
		return true
	}
	switch v := raw.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(v), email)
	case []any:
		if len(v) == 0 {
			return true
		}
		for _, item := range v {
			s, _ := item.(string)
			if strings.EqualFold(strings.TrimSpace(s), email) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func asRules(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if ok {
			out = append(out, m)
		}
	}
	return out
}

func deepCopyMap(in map[string]any) map[string]any {
	raw, err := json.Marshal(in)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}
