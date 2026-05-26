package research

import (
	"context"
	"strings"
)

// ClaimVerifier 负责对最终答案层面的 claim/evidence 绑定结果做校验和降级处理。
type ClaimVerifier interface {
	VerifyClaims(ctx context.Context, in ClaimVerificationInput) (Answer, error)
}

// ClaimVerificationInput 是最终 claim verification 阶段的输入。
type ClaimVerificationInput struct {
	// Question 是用户原始问题，后续模型 verifier 可以用它判断 claim 是否偏题。
	Question string
	// Answer 是已经经过 FinalSynthesizer 和 EvidenceBinder 的答案。
	Answer Answer
}

// RuleBasedClaimVerifier 使用 Answer.Evidence 做确定性校验。
//
// 第一版不调用模型 judge：unsupported claim 会从 KeyFindings 中移除并进入 limitations；
// 只有 source-level、没有 chunk/quote 的 claim 会保留但标记为弱支持。
type RuleBasedClaimVerifier struct{}

// VerifyClaims 返回校验后的 Answer。
func (v RuleBasedClaimVerifier) VerifyClaims(ctx context.Context, in ClaimVerificationInput) (Answer, error) {
	if err := ctx.Err(); err != nil {
		return in.Answer, err
	}
	if len(in.Answer.Evidence) == 0 {
		return in.Answer, nil
	}

	answer := in.Answer
	unsupported := make(map[string]ClaimEvidence)
	weak := make([]ClaimEvidence, 0)
	for _, evidence := range answer.Evidence {
		key := normalizedClaimKey(evidence.Claim)
		if key == "" {
			continue
		}
		if !evidence.Supported {
			unsupported[key] = evidence
			continue
		}
		if claimEvidenceIsWeak(evidence) {
			weak = append(weak, evidence)
		}
	}

	if len(unsupported) > 0 {
		answer.KeyFindings = removeUnsupportedKeyFindings(answer.KeyFindings, unsupported)
		for _, evidence := range unsupported {
			answer.Limitations = append(answer.Limitations, "Verified unsupported final claim: "+evidence.Claim)
		}
	}
	for _, evidence := range weak {
		answer.Limitations = append(answer.Limitations, "Weak evidence for final claim: "+evidence.Claim)
	}
	answer.Limitations = dedupeStrings(answer.Limitations)
	return answer, nil
}

// verifyFinalClaims 调用配置中的 claim verifier，并在 verifier 失败时保留原答案。
func (r *Runner) verifyFinalClaims(ctx context.Context, in ClaimVerificationInput) Answer {
	verifier := r.cfg.ClaimVerifier
	if verifier == nil {
		verifier = RuleBasedClaimVerifier{}
	}

	answer, err := verifier.VerifyClaims(ctx, in)
	if err != nil {
		in.Answer.Limitations = dedupeStrings(append(in.Answer.Limitations, "Claim verification skipped: "+err.Error()))
		return in.Answer
	}
	return answer
}

func removeUnsupportedKeyFindings(findings []string, unsupported map[string]ClaimEvidence) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		key := normalizedClaimKey(stripCitationText(finding))
		if _, ok := unsupported[key]; ok {
			continue
		}
		out = append(out, finding)
	}
	return out
}

func claimEvidenceIsWeak(evidence ClaimEvidence) bool {
	if len(evidence.EvidenceRefs) == 0 {
		return true
	}
	for _, ref := range evidence.EvidenceRefs {
		if strings.TrimSpace(ref.Quote) != "" || strings.TrimSpace(ref.ChunkID) != "" {
			return false
		}
	}
	return true
}

func normalizedClaimKey(claim string) string {
	claim = stripCitationText(claim)
	claim = strings.TrimSpace(strings.ToLower(claim))
	if claim == "" {
		return ""
	}
	return strings.Join(strings.Fields(claim), " ")
}
