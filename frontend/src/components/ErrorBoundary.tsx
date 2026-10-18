import { Component, type ReactNode, type ErrorInfo } from 'react'

/**
 * 全局错误边界
 *
 * 作用：任何页面组件抛异常时，只显示这一块的错误，而不是整站白屏。
 * 如果没有它，React 18 会卸载整棵树 → 用户看到纯白页面、没有任何信息。
 */
export default class ErrorBoundary extends Component<
  { children: ReactNode },
  { error: Error | null; info: string }
> {
  state = { error: null as Error | null, info: '' }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // 把堆栈打到控制台，方便排查
    console.error('[ErrorBoundary]', error, info.componentStack)
    this.setState({ info: info.componentStack || '' })
  }

  render() {
    if (!this.state.error) return this.props.children

    return (
      <div className="max-w-2xl mx-auto py-16 px-6">
        <div className="card border-l-[3px] border-l-red">
          <h2 className="text-lg font-bold text-red mb-2">这个页面出错了</h2>
          <p className="text-sm text-ink2 mb-4">
            不是你的操作问题，是页面渲染时遇到了异常。可以刷新重试，或返回上一页。
          </p>
          <pre className="bg-navy text-[#D9E2F0] p-3 rounded text-xs overflow-auto max-h-40 mb-4">
            {this.state.error.message}
          </pre>
          <div className="flex gap-2">
            <button className="btn btn-primary btn-sm" onClick={() => location.reload()}>
              刷新页面
            </button>
            <button className="btn btn-sm" onClick={() => (location.href = '/')}>
              返回项目列表
            </button>
          </div>
          <details className="mt-4">
            <summary className="text-xs text-ink3 cursor-pointer">技术细节</summary>
            <pre className="bg-[#F5F1E9] p-3 rounded text-[11px] overflow-auto max-h-48 mt-2 text-ink3">
              {this.state.info}
            </pre>
          </details>
        </div>
      </div>
    )
  }
}
