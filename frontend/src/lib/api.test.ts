import { beforeEach, afterEach, expect, test, vi } from 'vitest'
import { api, setToken, getToken } from './api'
const response = (status: number, data: unknown) => new Response(JSON.stringify(data), {status})
beforeEach(() => localStorage.clear())
afterEach(() => vi.unstubAllGlobals())
test('old 401 cannot log out a new account', async () => {
 let finish!: (v:Response)=>void
 vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(r => { finish=r })))
 setToken('account-A'); const pending=api.get('/projects'); setToken('account-B')
 finish(response(401,{ok:false,error:'expired'})); await expect(pending).rejects.toThrow()
 expect(getToken()).toBe('account-B')
})
test('old successful response is rejected after account switch', async () => {
 let finish!: (v:Response)=>void
 vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(r => { finish=r })))
 setToken('account-A'); const pending=api.get('/projects'); setToken('account-B')
 finish(response(200,{ok:true,data:{secret:'A'}})); await expect(pending).rejects.toThrow()
})
test('503 preserves session and carries retryable error', async () => {
 vi.stubGlobal('fetch', vi.fn(async()=>response(503,{ok:false,error:'temporarily unavailable'})))
 setToken('account-A'); await expect(api.get('/auth/me')).rejects.toMatchObject({status:503})
 expect(getToken()).toBe('account-A')
})
test('budget error exposes structured difference without parsing text', async () => {
 vi.stubGlobal('fetch', vi.fn(async()=>response(429,{ok:false,error:'budget',code:'budget_insufficient',requiredCredits:275,availableCredits:100})))
 await expect(api.post('/projects/p/shots/queue')).rejects.toMatchObject({code:'budget_insufficient',requiredCredits:275,availableCredits:100})
})

test('session_scope_survives_reload_but_rotates_on_token_change',async()=>{
 const {getSessionScope,clearToken}=await import('./api')
 setToken('A');const first=getSessionScope();expect(getSessionScope()).toBe(first);expect(localStorage.getItem('drama_session_revision')).toBe(first)
 setToken('A');expect(getSessionScope()).not.toBe(first)
 const second=getSessionScope();clearToken();expect(getSessionScope()).not.toBe(second)
})
test('cross_tab_revision_invalidates_inflight_response_even_with_same_token',async()=>{
 let finish!:(r:Response)=>void;vi.stubGlobal('fetch',()=>new Promise<Response>(r=>finish=r));setToken('same');const pending=api.get('/projects');localStorage.setItem('drama_session_revision','other-tab-new-login');window.dispatchEvent(new StorageEvent('storage',{key:'drama_session_revision'}));finish(response(200,{ok:true,data:{secret:'old-session'}}));await expect(pending).rejects.toMatchObject({code:'stale_response'})
})
