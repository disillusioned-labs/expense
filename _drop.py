import io
import re


def patch(path, repls, must=True):
    s = io.open(path, encoding='utf-8').read()
    for old, new in repls:
        if old not in s:
            if must:
                raise AssertionError((path, old[:70]))
            continue
        s = s.replace(old, new)
    io.open(path, 'w', encoding='utf-8', newline='').write(s)
    print('patched', path)


def drop_pattern(path, pat, flags=re.S):
    s = io.open(path, encoding='utf-8').read()
    new = re.sub(pat, '', s, count=1, flags=flags)
    assert new != s, (path, pat[:60])
    io.open(path, 'w', encoding='utf-8', newline='').write(new)
    print('dropped from', path)


# ---------- migration 00005: resubmitted_from ----------
patch('db/migrations/00005_transactions.sql', [
    ('''    resubmitted_from  UUID         NULL REFERENCES transactions(id), -- resubmit menunjuk transaksi lama yang rejected
''', ''),
])

# ---------- migration 00006: ocr_amount ----------
patch('db/migrations/00006_documents.sql', [
    ('''    ocr_amount       BIGINT       NULL, -- angka yang dibaca OCR; sumber kebenaran tetap konfirmasi user
''', ''),
])

# ---------- queries/transactions.sql ----------
drop_pattern('db/queries/transactions.sql',
             r'-- name: GetRejectedTransaction :one\n.*?\n\n')
patch('db/queries/transactions.sql', [
    ('''                          category, created_by, created_by_name, created_by_email, created_by_role,
                          resubmitted_from)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)''',
     '''                          category, created_by, created_by_name, created_by_email, created_by_role)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)'''),
    ('    submitted_at, decided_at, resubmitted_from, created_at, updated_at, deleted_at;',
     '    submitted_at, decided_at, created_at, updated_at, deleted_at;'),
    ('       submitted_at, decided_at, resubmitted_from, created_at, updated_at, deleted_at\n',
     '       submitted_at, decided_at, created_at, updated_at, deleted_at\n'),
    ('       t.submitted_at, t.decided_at, t.resubmitted_from, t.created_at, t.updated_at,',
     '       t.submitted_at, t.decided_at, t.created_at, t.updated_at,'),
])
drop_pattern('db/queries/transactions.sql',
             r'-- name: SumOcrAmounts :one\n.*?\n')

# ---------- queries/documents.sql ----------
patch('db/queries/documents.sql', [
    ('    ocr_status, ocr_data, ocr_amount, ocr_processed_at, uploaded_by, created_at, deleted_at;',
     '    ocr_status, ocr_data, ocr_processed_at, uploaded_by, created_at, deleted_at;'),
    ('       d.ocr_status, d.ocr_data, d.ocr_amount, d.ocr_processed_at, d.uploaded_by, d.created_at, d.deleted_at',
     '       d.ocr_status, d.ocr_data, d.ocr_processed_at, d.uploaded_by, d.created_at, d.deleted_at'),
    ('       ocr_status, ocr_data, ocr_amount, ocr_processed_at, uploaded_by, created_at, deleted_at',
     '       ocr_status, ocr_data, ocr_processed_at, uploaded_by, created_at, deleted_at'),
])

# ---------- queries/approvals.sql ----------
patch('db/queries/approvals.sql', [
    ('''       (SELECT COUNT(*) FROM documents d WHERE d.transaction_id = t.id AND d.deleted_at IS NULL) AS document_count,
       (SELECT COALESCE(SUM(d2.ocr_amount), 0)::BIGINT FROM documents d2
        WHERE d2.transaction_id = t.id AND d2.deleted_at IS NULL AND d2.ocr_amount IS NOT NULL)   AS sum_ocr_amount''',
     '''       (SELECT COUNT(*) FROM documents d WHERE d.transaction_id = t.id AND d.deleted_at IS NULL) AS document_count'''),
])

# ---------- errors.go ----------
patch('internal/service/errors.go', [
    ('var ErrResubmitInvalid = NewError("VALIDATION_FAILED", 422, "resubmitted_from must reference a rejected transaction in the same organization")\n', ''),
])

# ---------- service/transaction ----------
patch('internal/service/transaction/model.go', [
    ('type ConfirmOCRInput struct {', 'type ConfirmOCRInput struct {'),  # no-op anchor
])
drop_pattern('internal/service/transaction/model.go',
             r'\tResubmittedFrom \*uuid\.UUID\n')
patch('internal/service/transaction/model.go', [
    ('''	AmountFromDocuments int64           `json:"amount_from_documents"`
	AmountDiscrepancy   int64           `json:"amount_discrepancy"`
''', ''),
    ('\tOcrAmount *int64    `json:"ocr_amount"`\n', ''),
])

s = io.open('internal/service/transaction/service.go', encoding='utf-8').read()
old = '''	if in.ResubmittedFrom != nil {
		if _, err := s.repo.GetRejectedTransaction(ctx, repository.GetRejectedTransactionParams{
			ID:             *in.ResubmittedFrom,
			OrganizationID: actor.OrgID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Detail{}, service.ErrResubmitInvalid
			}
			return Detail{}, err
		}
	}
'''
assert old in s
s = s.replace(old, '')
old = '\t\t\tResubmittedFrom: in.ResubmittedFrom,\n'
assert old in s
s = s.replace(old, '')
old = '''	sum, err := s.repo.SumOcrAmounts(ctx, t.ID)
	if err != nil {
		return Detail{}, err
	}
	detail.AmountFromDocuments = sum
	detail.AmountDiscrepancy = t.TotalAmount - sum

'''
assert old in s
s = s.replace(old, '')
old = '''		if d.OcrAmount.Valid {
			v := d.OcrAmount.Int64
			brief.OcrAmount = &v
		}
'''
assert old in s
s = s.replace(old, '')
io.open('internal/service/transaction/service.go', 'w', encoding='utf-8', newline='').write(s)
print('patched internal/service/transaction/service.go')

# ---------- service/document ----------
patch('internal/service/document/model.go', [
    ('\tOcrAmount *int64    `json:"ocr_amount"`\n', ''),
])
s = io.open('internal/service/document/service.go', encoding='utf-8').read()
old = '''	if d.OcrAmount.Valid {
		v := d.OcrAmount.Int64
		out.OcrAmount = &v
	}
'''
if old in s:
    s = s.replace(old, '')
    io.open('internal/service/document/service.go', 'w', encoding='utf-8', newline='').write(s)
    print('patched internal/service/document/service.go')

# ---------- service/approval ----------
patch('internal/service/approval/model.go', [
    ('\tAmountDiscrepancy int64         `json:"amount_discrepancy"`\n', ''),
])
patch('internal/service/approval/engine.go', [
    ('\t\t\tAmountDiscrepancy: r.TotalAmount - r.SumOcrAmount,\n', ''),
])

# ---------- handlers ----------
patch('internal/handler/transaction/request.go', [
    ('\tResubmittedFrom *uuid.UUID    `json:"resubmitted_from"`\n', ''),
    ('\t\tResubmittedFrom: r.ResubmittedFrom,\n', ''),
])
patch('internal/handler/transaction/response.go', [
    ('\tOcrAmount *int64    `json:"ocr_amount"`\n', ''),
    ('\t\t\tOcrAmount: doc.OcrAmount,\n', ''),
    ('\tAmountFromDocuments int64                    `json:"amount_from_documents"`\n\tAmountDiscrepancy   int64                    `json:"amount_discrepancy"`\n', ''),
    ('\t\tAmountFromDocuments: d.AmountFromDocuments,\n\t\tAmountDiscrepancy:   d.AmountDiscrepancy,\n', ''),
])
patch('internal/handler/document/response.go', [
    ('\tOcrAmount *int64    `json:"ocr_amount"`\n', ''),
    ('\t\tOcrAmount: d.OcrAmount,\n', ''),
])
patch('internal/handler/approval/response.go', [
    ('\tAmountDiscrepancy int64         `json:"amount_discrepancy"`\n', ''),
    ('\t\tAmountDiscrepancy: a.AmountDiscrepancy,\n', ''),
])
