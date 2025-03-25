package utils

import (
    "time"
)

func AddOneMonth(date time.Time) time.Time {
    return date.AddDate(0, 1, 0)
}

func AddOneYear(date time.Time) time.Time {
    return date.AddDate(1, 0, 0)
}

func FormatDate(date time.Time) string {
    return date.Format("2006-01-02")
}

func ValidateDate(date string) bool {
    _, err := time.Parse("2006-01-02", date)
    return err == nil
}
