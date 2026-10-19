import {test,expect} from 'vitest'
import {render,screen} from '@testing-library/react'
import {Status} from '../features/admin/presentation'
import {Status as PartnerStatus} from '../features/partner/presentation'
import {Table,Cell} from '../features/workspace/ui'
test('pending_payment_differs_from_pending_receipt',()=>{render(<><Status value="pending" context="payment"/><Status value="pending" context="receipt"/><PartnerStatus value="paid"/></>);expect(screen.getByText('待支付')).toBeTruthy();expect(screen.getByText('待结算')).toBeTruthy();expect(screen.getByText('已支付')).toBeTruthy()})
test('workspace_mobile_preserves_all_actions_in_single_tree',()=>{render(<Table head={['订单','操作']}><tr><Cell label="订单">o</Cell><Cell label="操作"><button>查询结果</button></Cell></tr></Table>);expect(screen.getAllByRole('button',{name:'查询结果'})).toHaveLength(1);expect(screen.getByText('o').getAttribute('data-label')).toBe('订单')})
