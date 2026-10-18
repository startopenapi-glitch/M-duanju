/** 支付/保存成功的勾选动效（带 prefers-reduced-motion 降级） */
export default function SuccessCheck({ size = 48 }: { size?: number }) {
  return (
    <span className="t-success" data-state="in" aria-hidden="true">
      <svg width={size} height={size} viewBox="0 0 48 48" fill="none">
        <circle cx="24" cy="24" r="22" stroke="#3E6B4F" strokeWidth="2" opacity="0.25"/>
        <path d="M14 25l7 7 14-15" stroke="#3E6B4F" strokeWidth="3.5"
          strokeLinecap="round" strokeLinejoin="round"/>
      </svg>
    </span>
  )
}
