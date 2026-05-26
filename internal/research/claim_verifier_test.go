package research

import (
	"context"
	"strings"
	"testing"
)

func TestRuleBasedClaimVerifierRemovesUnsupportedKeyFinding(t *testing.T) {
	verifier := RuleBasedClaimVerifier{}
	answer, err := verifier.VerifyClaims(context.Background(), ClaimVerificationInput{
		Answer: Answer{
			KeyFindings: []string{
				"Eino supports composable workflows [src_1].",
				"Eino guarantees zero production incidents.",
			},
			Evidence: []ClaimEvidence{{
				Claim:        "Eino supports composable workflows.",
				Supported:    true,
				EvidenceRefs: []EvidenceRef{{SourceID: "src_1", ChunkID: "src_1_chunk_1", Quote: "Composable workflows."}},
			}, {
				Claim:     "Eino guarantees zero production incidents.",
				Supported: false,
				Reason:    "no matching evidence",
			}},
		},
	})
	if err != nil {
		t.Fatalf("VerifyClaims() error = %v", err)
	}
	if len(answer.KeyFindings) != 1 || strings.Contains(answer.KeyFindings[0], "zero production") {
		t.Fatalf("key findings = %#v, want unsupported finding removed", answer.KeyFindings)
	}
	found := false
	for _, limitation := range answer.Limitations {
		if strings.Contains(limitation, "Verified unsupported final claim") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("limitations = %#v, want unsupported verification limitation", answer.Limitations)
	}
}

func TestRuleBasedClaimVerifierMarksWeakEvidence(t *testing.T) {
	verifier := RuleBasedClaimVerifier{}
	answer, err := verifier.VerifyClaims(context.Background(), ClaimVerificationInput{
		Answer: Answer{
			KeyFindings: []string{"Eino has source-level evidence [src_1]."},
			Evidence: []ClaimEvidence{{
				Claim:        "Eino has source-level evidence.",
				SourceIDs:    []string{"src_1"},
				EvidenceRefs: []EvidenceRef{{SourceID: "src_1"}},
				Supported:    true,
			}},
		},
	})
	if err != nil {
		t.Fatalf("VerifyClaims() error = %v", err)
	}
	if len(answer.KeyFindings) != 1 {
		t.Fatalf("key findings = %#v, want weak supported claim preserved", answer.KeyFindings)
	}
	found := false
	for _, limitation := range answer.Limitations {
		if strings.Contains(limitation, "Weak evidence") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("limitations = %#v, want weak evidence limitation", answer.Limitations)
	}
}
