package main

import (
	"fmt"
)

func handleCompletion(shell string) {
	switch shell {
	case "bash":
		fmt.Print(bashCompletionScript())
	case "zsh":
		fmt.Print(zshCompletionScript())
	case "fish":
		fmt.Print(fishCompletionScript())
	default:
		fmt.Printf("Unsupported shell: %s. Supported: bash, zsh, fish\n", shell)
	}
}

func bashCompletionScript() string {
	return `_uplink_completions() {
    local cur prev opts
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    opts="send receive config clean queue watch help"

    case "${prev}" in
        send)
            COMPREPLY=( $(compgen -f -- "${cur}") )
            return 0
            ;;
        receive)
            return 0
            ;;
        config)
            COMPREPLY=( $(compgen -W "set reset" -- "${cur}") )
            return 0
            ;;
        queue)
            COMPREPLY=( $(compgen -W "pause resume cancel clear" -- "${cur}") )
            return 0
            ;;
    esac

    COMPREPLY=( $(compgen -W "${opts}" -- "${cur}") )
    return 0
}
complete -F _uplink_completions uplink
`
}

func zshCompletionScript() string {
	return `#compdef uplink
_uplink() {
    local line
    _arguments -C \
        "1: :((send receive config clean queue watch help))" \
        "*::arg:->args"

    case $line[1] in
        send)
            _files
            ;;
        config)
            _arguments "1: :((set reset))"
            ;;
        queue)
            _arguments "1: :((pause resume cancel clear))"
            ;;
    esac
}
_uplink "$@"
`
}

func fishCompletionScript() string {
	return `complete -c uplink -n "__fish_use_subcommand" -a "send" -d "Upload a file or directory"
complete -c uplink -n "__fish_use_subcommand" -a "receive" -d "Download a file or directory"
complete -c uplink -n "__fish_use_subcommand" -a "config" -d "Manage configuration"
complete -c uplink -n "__fish_use_subcommand" -a "clean" -d "Clean old states"
complete -c uplink -n "__fish_use_subcommand" -a "queue" -d "Queue operations"
complete -c uplink -n "__fish_use_subcommand" -a "watch" -d "Watch directory"
`
}
