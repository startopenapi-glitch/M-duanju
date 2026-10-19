import {test,expect,vi,afterEach} from 'vitest'
import {render,screen,fireEvent,waitFor} from '@testing-library/react'
import Credits from './Credits'
import {api} from '../lib/api'
afterEach(()=>vi.restoreAllMocks())
test('preserves deployed recharge tiers and sends unchanged integer cents',async()=>{
 vi.spyOn(api,'get').mockImplementation(async p=>p==='/credits'?{balance:0,transactions:[]}:p==='/orders'?{items:[]}:p==='/orders/o/progress'?{id:'o',payMode:'manual',status:'pending',credited:false,runId:null,projectId:null}:{enabled:true})
 const post=vi.spyOn(api,'post').mockResolvedValue({id:'o',downlevel:true})
 for(const [i,amount] of [100,500,3000].entries()){
  const page=render(<Credits/>)
  await waitFor(()=>expect(screen.getAllByRole('button',{name:'微信扫码支付'})[i].hasAttribute('disabled')).toBe(false))
  for (const value of [100,500,3000]) expect(screen.getByText('¥'+value)).toBeTruthy()
  expect(screen.queryByText('¥30')).toBeNull()
  expect(screen.getAllByText(/到账 .* 积分/)).toHaveLength(3)
  fireEvent.click(screen.getAllByRole('button',{name:'微信扫码支付'})[i])
  await waitFor(()=>expect(post).toHaveBeenLastCalledWith('/pay/create',{amountCents:amount*100,channel:'native'}))
  await screen.findByText('申请已保留，等待管理员审核。无需重复申请。')
  page.unmount()
 }
})
