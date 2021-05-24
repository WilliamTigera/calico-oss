package math

// MinIntGtZero returns the min of a and b, but ignoring values less than or equal to 0.
// Returns 0 iff both values <= 0.
func MinIntGtZero(a, b int) int {
	var rc int
	if a == 0 {
		rc = b
	} else if b == 0 {
		rc = a
	} else if a < b {
		rc = a
	} else {
		rc = b
	}
	if rc < 0 {
		return 0
	}
	return rc
}

// MinIntGtZero returns the max of a and b, but ignoring values less than or equal to 0.
// Returns 0 iff both values <= 0.
func MaxIntGtZero(a, b int) int {
	var rc int
	if a == 0 {
		rc = b
	} else if b == 0 {
		rc = a
	} else if a > b {
		rc = a
	} else {
		rc = b
	}
	if rc < 0 {
		return 0
	}
	return rc
}
