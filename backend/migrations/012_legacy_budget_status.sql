-- 旧代码曾把项目置为 BUDGET_EXCEEDED 这一终态；新代码把预算不足改为任务级
-- （job.settlement_budget），既不再产生也不再清除该状态，导致这些项目永久卡死。
-- 按实际进度归一，让用户能继续生成；只改状态，不动任何金额。
UPDATE projects p SET status = CASE
    WHEN EXISTS (SELECT 1 FROM shots s WHERE s.project_id = p.id AND s.status IN ('GENERATED','APPROVED')) THEN 'SHOTS_REVIEW'
    WHEN COALESCE(p.keyvisual_url, '') <> '' THEN 'KEYVISUAL_REVIEW'
    ELSE 'SCRIPT_REVIEW'
  END,
  error_msg = CASE
    WHEN COALESCE(p.error_msg, '') = '' THEN '历史「预算熔断」状态已归一，可继续生成'
    ELSE p.error_msg
  END,
  updated_at = now()
WHERE p.status = 'BUDGET_EXCEEDED';
