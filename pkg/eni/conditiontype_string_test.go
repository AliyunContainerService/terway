package eni

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConditionType_String_Full(t *testing.T) {
	ct := Full
	assert.Equal(t, "Full", ct.String())
}

func TestConditionType_String_ResourceTypeMismatch(t *testing.T) {
	ct := ResourceTypeMismatch
	assert.Equal(t, "ResourceTypeMismatch", ct.String())
}

func TestConditionType_String_NetworkInterfaceMismatch(t *testing.T) {
	ct := NetworkInterfaceMismatch
	assert.Equal(t, "NetworkInterfaceMismatch", ct.String())
}

func TestConditionType_String_InsufficientVSwitchIP(t *testing.T) {
	ct := InsufficientVSwitchIP
	assert.Equal(t, "InsufficientVSwitchIP", ct.String())
}

func TestConditionType_String_InvalidNegative(t *testing.T) {
	ct := ConditionType(-1)
	result := ct.String()
	// Should return "ConditionType(-1)" for invalid negative value
	assert.Equal(t, "ConditionType(-1)", result)
}

func TestConditionType_String_InvalidOutOfRange(t *testing.T) {
	ct := ConditionType(100)
	result := ct.String()
	// Should return "ConditionType(100)" for out of range value
	assert.Equal(t, "ConditionType(100)", result)
}

func TestConditionType_String_BoundaryAboveMax(t *testing.T) {
	// InsufficientVSwitchIP is 3, so 4 should be out of range
	ct := ConditionType(4)
	result := ct.String()
	assert.Equal(t, "ConditionType(4)", result)
}

func TestConditionType_String_AllValidValues(t *testing.T) {
	tests := []struct {
		ct       ConditionType
		expected string
	}{
		{Full, "Full"},
		{ResourceTypeMismatch, "ResourceTypeMismatch"},
		{NetworkInterfaceMismatch, "NetworkInterfaceMismatch"},
		{InsufficientVSwitchIP, "InsufficientVSwitchIP"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.ct.String())
		})
	}
}
