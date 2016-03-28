package lexer

import (
	"bytes"
	"fmt"
)

type State int

const (
	Normal State = iota
	Command
	CaseWaitWord
	CaseWaitIn
	CaseWaitPattern
)

const (
	specialSymbols = " \t#\n;&|()!{}<>\\\"$"
)

var keywords = map[string]struct{}{
	"case":  struct{}{},
	"do":    struct{}{},
	"done":  struct{}{},
	"elif":  struct{}{},
	"else":  struct{}{},
	"esac":  struct{}{},
	"fi":    struct{}{},
	"for":   struct{}{},
	"if":    struct{}{},
	"in":    struct{}{},
	"then":  struct{}{},
	"until": struct{}{},
	"while": struct{}{},
}

// Lexer is a splitter of the shell syntax.
type Lexer struct {
	Input    []byte
	consumed int
	buffered int
	state    State
}

func (l *Lexer) getChar() byte {
	return l.Input[l.buffered]
}

func (l *Lexer) incBuffer() bool {
	l.buffered += 1
	if l.buffered == len(l.Input) {
		return false
	}
	return true
}

func (l *Lexer) getBuffer() []byte {
	return l.Input[l.consumed : l.buffered]
}

func (l *Lexer) getBufferSize() int {
	return l.buffered - l.consumed
}

func (l *Lexer) setBufferSize(size int) int {
	if l.consumed+size >= len(l.Input) {
		l.buffered = len(l.Input)
		return l.getBufferSize()
	}
	l.buffered = l.consumed + size
	return l.getBufferSize()
}

func (l *Lexer) consume(size int, kind Kind) Leaf {
	leaf := Leaf{
		Kind: kind,
		Data: l.Input[l.consumed : l.consumed+size],
		pos:  Pos(l.consumed),
	}
	l.consumed += size
	l.buffered = l.consumed
	return leaf
}

func (l *Lexer) tryConsumeString(s string, kind Kind) (Leaf, bool) {
	oldBufSize := l.getBufferSize()
	newBufSize := l.setBufferSize(len(s))
	if newBufSize != len(s) {
		l.setBufferSize(oldBufSize)
		return Leaf{}, false
	}
	if string(l.getBuffer()) != s {
		l.setBufferSize(oldBufSize)
		return Leaf{}, false
	}
	return l.consume(l.getBufferSize(), kind), true
}

func (l *Lexer) consumeFunc(f func(c byte) bool, kind Kind) Leaf {
	ok := true
	for ok {
		c := l.getChar()
		if !f(c) {
			break
		}
		ok = l.incBuffer()
	}
	if l.getBufferSize() == 0 {
		panic("nothing consumed")
	}
	return l.consume(l.getBufferSize(), kind)
}

func (l *Lexer) consumeWhile(b []byte, kind Kind) Leaf {
	return l.consumeFunc(func(c byte) bool {
		return bytes.IndexByte(b, c) != -1
	}, kind)
}

func (l *Lexer) consumeUntil(b []byte, kind Kind) Leaf {
	return l.consumeFunc(func(c byte) bool {
		return bytes.IndexByte(b, c) == -1
	}, kind)
}

func (l *Lexer) getSubshellStringNode() (Node, error) {
	if l.consumed >= len(l.Input) {
		return nil, nil
	}

	if l.state != CaseWaitPattern && l.Input[l.consumed] == ')' {
		return nil, nil
	}
	return l.Get()
}

func (l *Lexer) getSubshellString() (Node, error) {
	lquote, ok := l.tryConsumeString("$(", Quote)
	if !ok {
		panic("expected $(")
	}

	prevState := l.state
	l.state = Normal
	defer func() {
		l.state = prevState
	}()

	var nodes []Node
	for {
		n, err := l.getSubshellStringNode()
		if err != nil {
			return nil, err
		}
		if n == nil {
			break
		}
		nodes = append(nodes, n)
	}
	rquote, ok := l.tryConsumeString(")", Quote)
	if !ok {
		panic("expected )")
	}

	return SubshellString{
		Lquote: lquote,
		Nodes:  nodes,
		Rquote: rquote,
	}, nil
}

func isVariableName(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z'
}

func (l *Lexer) getVariable() (Node, error) {
	if l.getChar() != '$' {
		panic("expected $")
	}
	if !l.incBuffer() {
		panic("unexpected EOF")
	}

	c := l.getChar()

	switch c {
	case '(':
		return l.getSubshellString()
	case '{':
		panic("TODO: ${...} not implemented") // FIXME
	}
	if !l.incBuffer() {
		panic("unexpected EOF")
	}

	ok := true
	for ok {
		c = l.getChar()
		if !isVariableName(c) {
			break
		}
		ok = l.incBuffer()
	}
	return l.consume(l.getBufferSize(), Variable), nil
}

func (l *Lexer) getQQStringNode() (Node, error) {
	if l.consumed >= len(l.Input) {
		return nil, nil
	}

	switch l.Input[l.consumed] {
	case '\\':
		if l.consumed+1 >= len(l.Input) {
			return l.consume(1, Escaped), nil
		}
		return l.consume(2, Escaped), nil
	case '"':
		return nil, nil
	case '$':
		return l.getVariable()
	}
	return l.consumeUntil([]byte("\\\"$"), Term), nil
}

func (l *Lexer) getQQString() (Node, error) {
	lquote, ok := l.tryConsumeString("\"", Quote)
	if !ok {
		panic("expected \"")
	}
	var nodes []Node
	for {
		n, err := l.getQQStringNode()
		if err != nil {
			return nil, err
		}
		if n == nil {
			break
		}
		nodes = append(nodes, n)
	}
	rquote, ok := l.tryConsumeString("\"", Quote)
	if !ok {
		panic("expected \"")
	}
	return QQString{
		Lquote: lquote,
		Nodes:  nodes,
		Rquote: rquote,
	}, nil
}

func (l *Lexer) getWordNode() (Node, error) {
	if l.consumed >= len(l.Input) {
		return nil, nil
	}

	switch l.Input[l.consumed] {
	case ' ', '\t', '#', '\n', ';', '&', '|', '(', ')':
		return nil, nil
	case '!', '{', '}':
		return l.consume(1, Term), nil
	case '<', '>':
		return nil, nil
	case '\\':
		if l.consumed+1 >= len(l.Input) {
			return l.consume(1, Escaped), nil
		}
		return l.consume(2, Escaped), nil
	case '"':
		return l.getQQString()
	case '$':
		return l.getVariable()
	}
	return l.consumeUntil([]byte(specialSymbols), Term), nil
}

func (l *Lexer) getWord() (Node, error) {
	var nodes []Node
	for {
		n, err := l.getWordNode()
		if err != nil {
			return nil, err
		}
		if n == nil {
			break
		}
		nodes = append(nodes, n)
	}
	return Word{
		Nodes: nodes,
	}, nil
}

// Get returns Node from unconsumed Input.
func (l *Lexer) Get() (Node, error) {
	if l.consumed >= len(l.Input) {
		return nil, nil
	}

	next := l.Input[l.consumed]

	switch next {
	case ' ', '\t':
		return l.consumeWhile([]byte(" \t"), Space), nil
	case '#':
		return l.consumeUntil([]byte("\n"), Comment), nil
	case '\n':
		if l.state == Command {
			l.state = Normal
		}
		return l.consume(1, NewLine), nil
	}

	if l.state == CaseWaitWord {
		lexeme, err := l.getWord()
		if err != nil {
			return nil, err
		}

		l.state = CaseWaitIn
		return lexeme, nil
	}

	if l.state == CaseWaitIn {
		switch next {
		case ';', '&', '|', '(', ')', '!', '{', '}', '<', '>', '\\', '"', '$':
			return nil, fmt.Errorf("unexpected %c", next)
		}

		lexeme := l.consumeUntil([]byte(specialSymbols), Keyword)
		if string(lexeme.Data) != "in" {
			return nil, fmt.Errorf("expected \"in\", got %q", lexeme.Data)
		}
		l.state = CaseWaitPattern
		return lexeme, nil
	}

	if l.state == CaseWaitPattern {
		switch next {
		case ';', '&', '|':
			return nil, fmt.Errorf("unexpected %c", next)
		case '(':
			return l.consume(1, Operator), nil
		case ')':
			l.state = Normal
			return l.consume(1, Operator), nil
		case '!', '{', '}', '<', '>', '\\', '"', '$':
			return nil, fmt.Errorf("unexpected %c", next)
		}

		lexeme := l.consumeUntil([]byte(specialSymbols), Term)
		if string(lexeme.Data) == "esac" {
			lexeme.Kind = Keyword
			l.state = Normal
		}
		return lexeme, nil
	}

	if l.state != Normal && l.state != Command {
		panic("unexpected state")
	}

	if next == ';' || next == '&' || next == '|' || next == '(' || next == ')' {
		l.state = Normal
		switch next {
		case ';':
			if leaf, ok := l.tryConsumeString(";;", Operator); ok {
				l.state = CaseWaitPattern
				return leaf, nil
			}
			return l.consume(1, Operator), nil
		case '&':
			if leaf, ok := l.tryConsumeString("&&", Operator); ok {
				return leaf, nil
			}
			return l.consume(1, Operator), nil
		case '|':
			if leaf, ok := l.tryConsumeString("||", Operator); ok {
				return leaf, nil
			}
			return l.consume(1, Operator), nil
		case '(', ')':
			return l.consume(1, Operator), nil
		default:
			panic("unexpected case")
		}
	}

	if next == '!' || next == '{' || next == '}' {
		lexeme, err := l.getWord()
		if err != nil {
			return nil, err
		}
		if l.state == Normal {
			if leaf, ok := lexeme.(Word).Leaf(Term); ok {
				if len(leaf.Data) == 1 {
					leaf.Kind = Operator
					return leaf, nil
				}
			}
			l.state = Command
		}
		return lexeme, nil
	}

	if next == '<' || next == '>' {
		l.state = Command
		switch next {
		case '<':
			for _, op := range []string{"<<-", "<<", "<>", "<&"} {
				if leaf, ok := l.tryConsumeString(op, Operator); ok {
					return leaf, nil
				}
			}
			return l.consume(1, Operator), nil
		case '>':
			for _, op := range []string{">>", ">&", ">|"} {
				if leaf, ok := l.tryConsumeString(op, Operator); ok {
					return leaf, nil
				}
			}
			return l.consume(1, Operator), nil
		default:
			panic("unexpected case")
		}
	}

	lexeme, err := l.getWord()
	if err != nil {
		return nil, err
	}
	if l.state == Normal {
		if leaf, ok := lexeme.(Word).Leaf(Term); ok {
			if _, ok := keywords[string(leaf.Data)]; ok {
				leaf.Kind = Keyword
				if string(leaf.Data) == "case" {
					l.state = CaseWaitWord
				}
				return leaf, nil
			}
		}
		l.state = Command
	}
	return lexeme, nil
}
