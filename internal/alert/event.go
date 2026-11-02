// Package alert defines DLP alert notifications. Alerts never carry prompt or
// response content.
package alert

import "time"

// Alert is one DLP notification destined for email delivery.
type Alert struct {
	RuleName  string
	Employee  string
	Action    string
	RequestID string
	At        time.Time
}
