import { useEffect, useState, useRef, type ReactNode } from 'react'

import {useFocusScope} from '../layout/useFocusScope'

/**
 * 带进出过渡的弹层
 * 关闭时先跑 150ms 缩回动画再卸载，避免"啪"地消失
 */
export default function Modal({
  open, onClose, children, width = 'max-w-md',
}: { open: boolean; onClose: () => void; children: ReactNode; width?: string }) {
  const [mounted, setMounted] = useState(open)
  const [visible, setVisible] = useState(false)

  useEffect(() => {
    if (open) {
      setMounted(true)
      // 下一帧再加 is-open，让 transition 生效
      const t = requestAnimationFrame(() => setVisible(true))
      return () => cancelAnimationFrame(t)
    }
    // 关闭：先动画再卸载
    setVisible(false)
    const t = setTimeout(() => setMounted(false), 160)
    return () => clearTimeout(t)
  }, [open])

  const ref=useRef<HTMLDivElement>(null)
  useFocusScope(ref,mounted,onClose)

  if (!mounted) return null

  return (
    <div
      className={`fixed inset-0 bg-black/70 flex items-center justify-center z-50 p-6 transition-opacity duration-150 ${visible ? 'opacity-100' : 'opacity-0'}`}
      onClick={onClose}>
      <div
        ref={ref} role="dialog" aria-modal="true" aria-label="操作对话框" tabIndex={-1}
        className={`t-modal bg-paper rounded w-full ${width} ${visible ? 'is-open' : ''}`}
        onClick={e => e.stopPropagation()}>
        {children}
      </div>
    </div>
  )
}
