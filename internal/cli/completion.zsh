#compdef wip

# Workstream IDs from `wip _ids`, described by "STATE  TITLE". Open items by
# default; with --all, done, dropped and archived ones too.
_wip_ids() {
  local line
  local -a all fields ids
  # _arguments passes compadd options (-J group, ...) along with ours.
  zparseopts -D -E -- -all=all
  for line in ${(f)"$(_call_program ids wip _ids $all 2>/dev/null)"}; do
    fields=("${(@ps:\t:)line}")
    ids+=("${fields[1]}:${fields[2]}  ${fields[3]}")
  done
  _describe -t ids 'workstream' ids "$@"
}

# Scopes already in use, from `wip _scopes`.
_wip_scopes() {
  local -a scopes=(${(f)"$(_call_program scopes wip _scopes 2>/dev/null)"})
  _describe -t scopes 'scope' scopes "$@"
}

_wip() {
  local curcontext=$curcontext state line ret=1
  typeset -A opt_args

  local whens='(today yesterday mon tue wed thu fri sat sun)'

  _arguments -C \
    '1: :->cmd' \
    '*:: :->args' && return 0

  case $state in
    cmd)
      local -a cmds=(
        'ls:list workstreams'
        'show:print a workstream'
        'here:workstreams bound to this directory'
        'report:log entries since a date'
        'lint:stale items, missing paths, broken files'
        'new:create a workstream'
        'log:append a log entry'
        'status:set the short status'
        'wait:mark as waiting on someone'
        'resume:back to active'
        'done:mark done'
        'drop:mark dropped'
        'block:add a blocker'
        'unblock:remove blockers'
        'set:change fields'
        'edit:open in $EDITOR'
        'prune:archive old done/dropped items'
        'completion:print shell completion'
        'help:show usage'
        'version:print the version'
      )
      _describe -t commands 'wip command' cmds && ret=0
      ;;
    args)
      case $words[1] in
        ls)
          _arguments \
            '--scope[only this scope]:scope:_wip_scopes' \
            '--repo[only items with this repo]:repo' \
            '--state[states, comma-separated]:state:_sequence compadd - active waiting done dropped all' \
            '--waiting[waiting on]:who:(me others)' \
            '--archived[include archived items]' && ret=0
          ;;
        show)
          _arguments '1:workstream:_wip_ids --all' && ret=0
          ;;
        report)
          _arguments \
            "--since[from this date]:when:$whens" \
            '--scope[only this scope]:scope:_wip_scopes' \
            '--repo[only items with this repo]:repo' && ret=0
          ;;
        new)
          _arguments \
            '--scope[scope]:scope:_wip_scopes' \
            '--id[ID instead of one derived from the title]:id' \
            '--here[bind to the current git worktree]' \
            '--here-repo[bind to the whole repo, all worktrees]' \
            '*--repo[repo name]:repo' \
            '*--path[bound path]:path:_directories' \
            '--issue[issue URL]:url' \
            '*--link[link URL]:url' \
            '--doc[handoff or tracker doc]:doc:_files' \
            '--status[short status]:status' \
            '*:title' && ret=0
          ;;
        log|status|block)
          _arguments '1:workstream:_wip_ids' '*:text' && ret=0
          ;;
        wait)
          _arguments '1:workstream:_wip_ids' '2:who:(me)' '*:reason' && ret=0
          ;;
        resume)
          _arguments '1:workstream:_wip_ids --all' && ret=0
          ;;
        done|drop)
          _arguments '1:workstream:_wip_ids' '*:note' && ret=0
          ;;
        unblock|edit)
          _arguments '1:workstream:_wip_ids' && ret=0
          ;;
        set)
          _arguments \
            '1:workstream:_wip_ids' \
            '--title[title]:title' \
            '--scope[scope]:scope:_wip_scopes' \
            '--here[bind to the current git worktree]' \
            '--here-repo[bind to the whole repo, all worktrees]' \
            '*--repo[add repo]:repo' \
            '*--rm-repo[remove repo]:repo' \
            '*--path[add path]:path:_directories' \
            '*--rm-path[remove path]:path:_directories' \
            '--issue[issue URL, empty to clear]:url' \
            '*--link[add link]:url' \
            '*--rm-link[remove link]:url' \
            '--doc[doc path, empty to clear]:doc:_files' && ret=0
          ;;
        prune)
          _arguments \
            '--older[age, e.g. 30d or 4w]:age' \
            '--dry-run[only print what would be archived]' && ret=0
          ;;
        completion)
          _arguments '1:shell:(zsh)' && ret=0
          ;;
        *)
          _message 'no more arguments'
          ;;
      esac
      ;;
  esac
  return ret
}

_wip "$@"
