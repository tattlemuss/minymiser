	opt		d+				;Debug symbols

main:
	; Allocate memory
	movea.l	a7,a5
	movea.l	4(a5),a5			;base page
	move.l	12(a5),d0			;text size
	add.l	20(a5),d0			;data size
	add.l	28(a5),d0			;bss size
	addi.l	#$0500,d0			;stack size
	move.l	d0,d1				;copy total size
	add.l	a5,d1				;plus base size
	and.l	#-2,d1				;align
	movea.l	d1,a7				;new stack address
	move.l	d0,-(a7)			;size
	move.l	a5,-(a7)			;base of our prog
	clr.w	-(a7)				;dummy parameter
	move.w	#$4a,-(a7)			;mshrink()
	trap	#1				;call gemdos
	lea	12(a7),a7			;fix stack

	; Run core
	pea	modplay_loop
	move.w	#38,-(sp)
	trap	#14
	addq.l	#6,sp

	; Core:
	; Call func to set up player system
	; Call func to set a specific module to play
	; Start playback

	; (wait for keypress)
	; Stop playback
	; Clear player system
	; Exit

	clr.w	-(sp)				;pterm
	trap	#1				;call gemdos

modplay_loop:

.waitstart:
	;cmp.b 	#$2,$fffffc02.w
	;bne.s	.waitstart

	lea	player_data,a0
	bsr	ymp_player_init

	; Install timer C as test
	or.w	#$0700,sr			;disable interrupts
	move.l	$114.w,old_c
	move.l	#c_routine,$114.w
	move.b	#%1000000,$17(a0)		; irq vector register (automat. eoi)
	clr.b	$484.w
	move.w	#$2300,sr			; interrupts on

.key:	
	cmp.b 	#$39,$fffffc02.w
	bne.s	.key

	; :TODO: restore old systems
	move.l	old_c,$114.w
	rts

;-------------
c_routine
	move.w	#$2500,sr			; Allow other MFP interrupts (ikbd, border) to run

	sub.w	#50,tccount			; Allow variable play speed (here TC=50)
	bpl.s	.skip
	add.w	#200,tccount
	movem.l	d0-a6,-(a7)
	bsr	ymp_player_update
	move.w	#$777,$ffff8240.w
	movem.l	(a7)+,d0-a6
.skip:	move.l	old_c,-(sp)
	rts

tccount			dc.w	200		;timer C down counter
old_c:			ds.l	1


; -----------------------------------------------------------------------
;	CORE PLAYER CODE
; -----------------------------------------------------------------------
NUM_REGS		equ	14
YMP_CACHE_SIZE		equ	512			; number of saved bytes per register

							; KEEP THESE 3 IN ORDER
ymunp_match_read_ptr	equ	0			; X when copying, the src pointer (either in cache or in original stream)
ymunp_stream_read_ptr	equ	4			; position in packed data we are reading from
ymunp_copy_count_w	equ	8			; number of bytes remaining to copy. Decremented at start of update.
ymunp_size		equ	10			; structure size

; -----------------------------------------------------------------------
; a0 = start of packed ym data
ymp_player_init:
	move.l	a0,ymp_tune_ptr
ymp_player_restart:
	; "globals" first
	lea	ymp_cache(pc),a2
	move.l	a2,ymp_cache_write_ptr
	move.w	#YMP_CACHE_SIZE,ymp_cache_countdown

	lea	ymp_streams_state(pc),a1
	; a1 = state data
	move.l	a0,a3
	move.w	(a0)+,ymp_vbl_countdown

	; a3 = copy of packed file start
	moveq.l	#NUM_REGS-1,d0
.fill:
	; a0 = input data (this moves for each channel)
	move.l	a3,d1
	add.l	(a0)+,d1				; read size offset in header
	move.l	d1,ymunp_stream_read_ptr(a1)		; setup ymunp_stream_read_ptr
	clr.l	ymunp_match_read_ptr(a1)		; setup ymunp_match_read_ptr
	move.w	#1,ymunp_copy_count_w(a1)		; setup ymunp_copy_count_w
	lea	ymunp_size(a1),a1			; next stream state
	dbf	d0,.fill
	rts

; -----------------------------------------------------------------------
; a0 = input structure
ymp_player_update:
	move.w	#$700,$ffff8240.w
	lea	ymp_streams_state(pc),a0		; a0 = streams state
	lea	ymp_output_buffer(pc),a6		; a6 = YM buffer
	move.l	ymp_cache_write_ptr(pc),a2		; a2 = cache write ptr
	lea	ymp_cache+YMP_CACHE_SIZE(pc),a3		; a3 = cache end ptr (constant!)
	moveq	#NUM_REGS-1,d1				; d1 = loop counter
	move.w	#ymunp_size,d2				; d2 = stream structure size
	move.w	#YMP_CACHE_SIZE,d3			; d3 = cache size
ymp_stream_update:
	; a0	= ymunp struct
	subq.w	#1,ymunp_copy_count_w(a0)
	bne.s	.stream_copy_one			; still in copying state

	; Set up next ymunp_match_read_ptr and ymunp_copy_count_w here
	move.l	ymunp_stream_read_ptr(a0),a1		; a1 = packed data stream
	moveq	#0,d0
	move.b	(a1)+,d0
	; Match or reference?
	bclr	#7,d0
	bne.s	.literals

	; Match code
	; a1 is the stream read ptr
	; d0 is the pre-read count value
	bsr.s	read_extended_number
	move.w	d0,ymunp_copy_count_w(a0)

	; Now read offset
	moveq	#0,d0
	move.b	(a1)+,d0
	bsr.s	read_extended_number
	move.l	a1,ymunp_stream_read_ptr(a0)		; remember stream ptr now, before trashing a1

	; Apply offset backwards from where we are writing
	move.l	a2,a1					; current cache write ptr
	add.w	d3,a1					; add cache size
							; this value is still modulo "cache offset"
	sub.l	d0,a1					; apply reverse offset
	cmp.l	a3,a1					; past cache end?
	blt.s	.ptr_ok
	sub.w	d3,a1					; subtract cache size again
.ptr_ok:
	move.l	a1,ymunp_match_read_ptr(a0)
	bra.s	.stream_copy_one
.literals:
	; Literals code -- just a count
	; a1 is the stream read ptr
	; d0 is the pre-read count value
	bsr.s	read_extended_number
	move.w	d0,ymunp_copy_count_w(a0)
	move.l	a1,ymunp_match_read_ptr(a0)		; use the current packed stream address
	add.l	d0,a1					; skip bytes in input stream
	move.l	a1,ymunp_stream_read_ptr(a0)
	; Falls through to do the copy

.stream_copy_one:
	; Copy byte from either the cache or the literals in the stream
	move.l	ymunp_match_read_ptr(a0),a1		; a1 = match read
	; a2 = cache write, a3 = loop addr
	move.b	(a1)+,d0				; d0 = output result
	move.b	d0,(a2)					; add to cache. Don't need to increment

	; Handle the *read* pointer hitting the end of the cache
	; The write pointer check is done in one single go since all sizes are the same
	; This check is done even if literals are copied, it just won't ever pass the check
	cmp.l	a3,a1					; has match read ptr hit end of cache?
	bne.s	.noloop_cache_read
	sub.w	d3,a1					; move back in cache
.noloop_cache_read:
	move.l	a1,ymunp_match_read_ptr(a0)

	; d0 is "output" here
	move.b	d0,(a6)+				; write to output buffer

	; Move on to the next register
	add.w	d3,a2					; next ymp_cache_write_ptr
	add.w	d3,a3					; next cache_end ptr
	add.w	d2,a0					; next stream structure
	dbf	d1,ymp_stream_update
	bra.s	ym_write

; If the previous byte read was 0, read 2 bytes to generate a 16-bit value
read_extended_number:
	tst.b	d0
	bne.s	valid_count
	move.b	(a1)+,d0
	lsl.w	#8,d0
	move.b	(a1)+,d0
valid_count:
	rts

ym_write:
	move.w	#$007,$ffff8240.w

	; We could write these in reverse order and reuse a6?
	lea	ymp_output_buffer(pc),a6
	lea	$ffff8800.w,a0
	lea	$ffff8802.w,a1

r	set	0
	rept	NUM_REGS
	ifne	r-13					; Buzzer envelope
		move.b	#r,(a0)
		move.b	(a6)+,(a1)
	else
		; Buzzer variant
		move.b	(a6)+,d0			; Buzzer envelope register is special case,
		bmi.s	.skip_write
		move.b	#r,(a0)				; only write if value is not -1
		move.b	d0,(a1)				; since writing re-starts the envelope
.skip_write:
	endif
r	set	r+1
	endr

	; Update the "write to cache" variables
	addq.l	#1,ymp_cache_write_ptr
	subq.w	#1,ymp_cache_countdown
	bne.s	.no_cache_loop
	move.w	#YMP_CACHE_SIZE,ymp_cache_countdown
	; Roll base cache_write pointers
	sub.l	#YMP_CACHE_SIZE,ymp_cache_write_ptr
.no_cache_loop:
	; Check for tune restart
	subq.w	#1,ymp_vbl_countdown
	bne.s	.no_tune_restart
	move.l	ymp_tune_ptr(pc),a0
	; This should rewrite the countdown value and
	; all internal variables
	bsr	ymp_player_restart
.no_tune_restart:
	rts
; -----------------------------------------------------------------------
		even
; -----------------------------------------------------------------------
;
ymp_tune_ptr:		ds.l	1
ymp_cache_write_ptr:	ds.l	1
ymp_cache_countdown:	ds.w	1		; countdown to looping of cache ptrs
ymp_streams_state:	ds.b	ymunp_size*NUM_REGS
ymp_vbl_countdown:	ds.w	1		; number of VBLs left to restart
ymp_output_buffer:	ds.b	NUM_REGS
ymp_cache:		ds.b	YMP_CACHE_SIZE*NUM_REGS
			even

; Our packed data file.
player_data:		incbin	test_output/tomsdiner.ym3.cpp.ymp
			even
