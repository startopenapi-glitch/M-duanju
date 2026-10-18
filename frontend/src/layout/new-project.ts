import { createContext, useContext } from "react";
// 唯一跨界桥：任何页面都能唤起「新建短片」弹窗，无需知道外壳存在
export const NewProjectContext = createContext<() => void>(() => {});
export const useNewProject = () => useContext(NewProjectContext);
