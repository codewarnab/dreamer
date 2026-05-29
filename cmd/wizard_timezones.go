package cmd

import "github.com/charmbracelet/bubbles/list"

// timezoneItems returns common timezone entries plus a "custom..." option.
func timezoneItems() []list.Item {
	return []list.Item{
		selectItem{id: "Asia/Kolkata", desc: "IST, UTC+5:30"},
		selectItem{id: "America/New_York", desc: "ET, UTC-5 / EDT UTC-4"},
		selectItem{id: "America/Chicago", desc: "CT, UTC-6 / CDT UTC-5"},
		selectItem{id: "America/Denver", desc: "MT, UTC-7 / MDT UTC-6"},
		selectItem{id: "America/Los_Angeles", desc: "PT, UTC-8 / PDT UTC-7"},
		selectItem{id: "Europe/London", desc: "GMT, UTC+0 / BST UTC+1"},
		selectItem{id: "Europe/Berlin", desc: "CET, UTC+1 / CEST UTC+2"},
		selectItem{id: "Europe/Paris", desc: "CET, UTC+1 / CEST UTC+2"},
		selectItem{id: "Asia/Tokyo", desc: "JST, UTC+9"},
		selectItem{id: "Asia/Shanghai", desc: "CST, UTC+8"},
		selectItem{id: "Asia/Singapore", desc: "SGT, UTC+8"},
		selectItem{id: "Australia/Sydney", desc: "AEST, UTC+10 / AEDT UTC+11"},
		selectItem{id: "UTC", desc: "UTC+0"},
		selectItem{id: "custom", desc: "type any IANA timezone"},
	}
}
