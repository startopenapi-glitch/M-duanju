import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import ErrorBoundary from './ErrorBoundary'
import { useAuth } from '../App'
import { clearToken, api } from '../lib/api'

export default function Layout() {
  const { user, setUser } = useAuth()
  const nav = useNavigate()

  const logout = () => {
    clearToken(); setUser(null); nav('/login')
  }

  return (
    <div className="min-h-screen flex flex-col">
      <header className="border-b-2 border-ink bg-paper">
        <div className="max-w-6xl mx-auto px-6 py-4 flex items-center justify-between gap-4">
          <div className="flex items-baseline gap-3">
            <h1 className="text-xl font-bold tracking-tight">M-duanju</h1>
            <span className="text-xs text-ink3">AI 一键成片</span>
          </div>
          {user && (
            <nav className="flex items-center gap-1 text-sm">
              <NavLink to="/" end className={({isActive}) => `px-3 py-1.5 rounded font-medium ${isActive ? 'bg-ink text-[#F5F1E9]' : 'text-ink2 hover:bg-white'}`}>我的项目</NavLink>
              <NavLink to="/credits" className={({isActive}) => `px-3 py-1.5 rounded font-medium ${isActive ? 'bg-ink text-[#F5F1E9]' : 'text-ink2 hover:bg-white'}`}>
                积分 <span className="tabular-nums text-amberdeep font-semibold">{user.credits}</span>
              </NavLink>
              {user.role === 'admin' && (
                <div className="relative group">
                  <button className="px-3 py-1.5 rounded font-medium text-ink2 hover:bg-white">管理后台 ▾</button>
                  <div className="absolute right-0 top-full pt-1 hidden group-hover:block z-50">
                    <div className="bg-card border border-line rounded shadow-lg py-1 w-40">
                      <NavLink to="/admin/stats" className="block px-4 py-2 text-sm hover:bg-ambersoft">数据总览</NavLink>
                      <NavLink to="/admin/users" className="block px-4 py-2 text-sm hover:bg-ambersoft">用户管理</NavLink>
                      <NavLink to="/admin/orders" className="block px-4 py-2 text-sm hover:bg-ambersoft">充值审核</NavLink>
                    </div>
                  </div>
                </div>
              )}
              <NavLink to="/security" className={({isActive}) => `px-3 py-1.5 rounded font-medium ${isActive ? 'bg-ink text-[#F5F1E9]' : 'text-ink2 hover:bg-white'}`}>安全设置</NavLink>
              <span className="ml-2 text-xs text-ink3">{user.email}</span>
              <button onClick={logout} className="btn btn-sm ml-2">退出</button>
            </nav>
          )}
        </div>
      </header>
      <main className="flex-1 max-w-6xl w-full mx-auto px-6 py-8">
        <ErrorBoundary><Outlet/></ErrorBoundary>
      </main>
    </div>
  )
}
