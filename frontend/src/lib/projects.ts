export {
  PROJECTS_EVENT,
  useSharedProjects as useProjects,
} from "../features/creation/ProjectsProvider";
export function projectsChanged() {
  window.dispatchEvent(new Event("drama:projects-changed"));
}
