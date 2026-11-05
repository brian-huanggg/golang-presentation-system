# ADR 0016: Password policy as a pure validator, with NFKC normalization

| Item | Value |
|---|---|
| Status | Proposed |
| Date | 2026-11-05 |
| Deciders | Architect (to confirm); proposed by the developer of CUR-57 |
| Related | PRD REG-005, REG-007, AUTH-001, AUTH-007, NFR-SEC-003, AC-REG-005; Architecture 9.1; ADR 0005, planned ADR 0008 |

> Numbered 0016 because 0004, 0006, 0008…0013 and 0015 are reserved in Architecture Section 12. Architecture Section 12 does not list this ADR yet.

## Context

REG-005 sets the password policy: at least 12 characters; one uppercase letter, one lowercase letter and one symbol; no run of 3 or more consecutive letters or digits; no character repeated 3 or more times in a row. It applies at registration and at password change (REG-007). Architecture 9.1 says the validator lives in `auth` and is shared by both.

Three problems appeared while implementing it:

1. **Look-alike characters dodge the rules.** `ａｂｃ` and `１２３` (full-width) are visibly sequences, but a validator that compares ASCII code points does not see them.
2. **The same text has several encodings.** `é` can be one code point (NFC) or `e` plus U+0301 (NFD). Different devices and input methods produce different forms. Argon2id hashes bytes, so a password registered on one device would not verify when typed on another.
3. **The size bound and the normalized size differ.** The input limit exists to bound hashing work (`maxPasswordBytes`, 128). Normalization can expand text a lot (U+FDFA is 3 bytes and becomes 18 characters).

## Decision

**1. The policy is a pure function in `auth`.** `passwordViolations(pw) []string` has no database, clock or context dependency and returns the code of **every** violated rule in a fixed order (`password_too_short`, `password_no_upper`, `password_no_lower`, `password_no_symbol`, `password_sequence`, `password_repeat`). `checkPassword` adds `required` and `too_long`. The registration validator turns each code into one `FieldError` on `password`. The codes are the stable contract for the frontend (I18N-003: messages are English only, the frontend maps the code). REG-007 calls the same function.

**2. Rule definitions.**

- Minimum length counts characters (runes). Maximum length is 128 **bytes**, because it bounds Argon2id work.
- A symbol is a character for which `unicode.IsPunct` or `unicode.IsSymbol` is true. A space is not a symbol.
- Uppercase and lowercase use `unicode.IsUpper` and `unicode.IsLower`, so CJK characters count as neither.
- A sequence is 3 or more consecutive ASCII letters (case-insensitive) or digits, ascending or descending. There is no wrap-around (`890`, `yza`) and letters and digits never mix (`c12`).
- A repeat is the same character 3 or more times in a row, compared exactly (`aAa` is not a repeat).

**3. Normalize with Unicode NFKC before judging and before hashing.** `normalizePassword` (`golang.org/x/text/unicode/norm`, `norm.NFKC`) is applied in `passwordViolations`, `checkPassword`, `Argon2idHasher.Hash` and `verifyPassword`. The policy and the hash therefore see the same string, whatever form the client sent.

**4. The 128-byte bound is checked twice.** On the raw input first, so a huge string is never normalized. Again on the normalized string, so expansion cannot inflate hashing work.

**5. The normalization form is part of the stored-hash contract.** The hash input is the NFKC form. Changing the form later changes the input of every future verification and makes existing hashes fail.

## Alternatives considered

- **No normalization, ASCII-only rules.** Simplest, but `ａｂｃ` passes and NFC/NFD users may be locked out on another device. Rejected.
- **Make the sequence and repeat checks Unicode-aware instead of normalizing** (use `unicode.IsLetter`/`IsDigit` and fold full-width forms). Fixes the first problem only; the NFC/NFD hash mismatch remains.
- **NFC instead of NFKC.** Fixes the hash mismatch and is lossless, but does not fold full-width characters, so look-alikes still dodge the rules.
- **Return only the first violation.** Smaller change, but the user fixes rules one round trip at a time and the frontend loses information.
- **Enforce the policy in the frontend only.** Not a control; the API must enforce it (NFR-SEC-001 spirit, REG-005 is a Must).
- **A password-strength library (for example zxcvbn).** Would add a dependency and change the rules from the PRD's explicit list to a score. Not in scope.

## Consequences

- NFKC is lossy. Full-width and half-width forms, and compatibility forms such as the `ﬁ` ligature, are the same password. Users cannot tell them apart. This is acceptable because the alternative is a password that works on one device only.
- **Never change the normalization form without a migration plan.** A later change would need verification against both forms and a re-hash at next login. This ADR is the record of that constraint.
- Login (AUTH-001) must verify through `verifyPassword`, or otherwise through `normalizePassword`. A separate comparison path would reintroduce the mismatch. This is checked when the login ticket lands.
- `golang.org/x/text` becomes a direct dependency of the backend.
- Hashing a normalized string means hash input is not byte-for-byte what the user sent. This is not visible to users and does not affect the stored PHC format.
- The error response lists every violated rule and never contains the password. Rule text is public (PRD, `openapi.yaml`), so listing rules discloses nothing secret.
- `FieldError.code` is a free string in the OpenAPI contract. The new codes are documented in the `password` field description but are not an enum, so the generated frontend types cannot check them exhaustively.

## Open points

- Sequences ignore case while repeats do not (`aBc` is rejected, `aAa` is accepted). This follows the PRD text literally ("consecutive letters", "same character"). The Product Owner should confirm the intent; changing it touches only `hasRepeat` and its test table.
- This ADR does not cover Argon2id parameters, which stay with the planned ADR 0008.
