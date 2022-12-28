	opt		d+				;Debug symbols

main:
	; Allocate memory
	movea.l	a7,a5
	movea.l	4(a5),a5		;base page
	move.l	12(a5),d0		;text size
	add.l	20(a5),d0		;data size
	add.l	28(a5),d0		;bss size
	addi.l	#$0500,d0		;stack size
	move.l	d0,d1			;copy total size
	add.l	a5,d1			;plus base size
	and.l	#-2,d1			;align
	movea.l	d1,a7			;new stack address
	move.l	d0,-(a7)		;size
	move.l	a5,-(a7)		;base of our prog
	clr.w	-(a7)			;dummy parameter
	move.w	#$4a,-(a7)		;mshrink()
	trap	#1				;call gemdos
	lea	12(a7),a7		;fix stack

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

	clr.l	-(sp)			;pterm
	trap	#1				;call gemdos

modplay_loop:

.waitstart:	
	;cmp.b 	#$2,$fffffc02.w
	;bne.s	.waitstart

	lea	player_data,a0
	bsr	player_init

	; Install timer C as test
	or.w	#$0700,sr			;disable interrupts
	move.l	$114.w,old_c
	move.l	#c_routine,$114.w
	move.b	#%1000000,$17(a0)		; irq vector register (automat. eoi)

	clr.b	$484.w
	move.w	#$2300,sr			; interrupts on

;	move.w	#1000,d7
;.unp_loop:
;	bsr	player_update
;	dbf	d7,.unp_loop

.key:	
	cmp.b 	#$39,$fffffc02.w
	bne.s	.key

	; :TODO: restore old systems
	move.l	old_c,$114.w
	rts

;-------------
c_routine
	move.w	#$2500,sr			; Allow other MFP interrupts (ikbd, border) to run

	sub.w	#50,tccount
	bpl.s	.skip
	add.w	#200,tccount
	movem.l	d0-a6,-(a7)
	lea	player_state(pc),a0
	lea	output_buffer,a6
	eor.w	#$ff,$ffff8240.w
	bsr	player_update
	eor.w	#$ff,$ffff8240.w
	movem.l	(a7)+,d0-a6
.skip
	move.l	old_c,-(sp)
	rts

tccount	dc.w	200			;timer C down counter... this is a bit severe!
old_c:	ds.l	1

; -----------------------------------------------------------------------
NUM_REGS		equ	14
cache_size		equ	512		; number of saved bytes per register

						; KEEP THESE 3 IN ORDER
ymunp_match_read_ptr	equ	0		; X when copying, the src pointer (either in cache or in original stream)
ymunp_cache_write_ptr	equ	4		; X current next write address of cache
ymunp_cache_end_ptr	equ	8		; X end address of cache
ymunp_stream_read_ptr	equ	12		; position in packed data we are reading from
ymunp_cache_start_ptr	equ	16		; start address of our cache
ymunp_cache_size_w	equ	20		; X size of cache in bytes
ymunp_size_count_w	equ	22		; number of bytes remaining to copy. Decremented at start of update.
ymunp_size		equ	24		; structure size

player_init:
	lea	player_state(pc),a1
	lea	cache(pc),a2
	; a1 = state data
	; a2 = cache
	move.l	a0,a3
	; a3 = copy of packed file start
	moveq.l	#NUM_REGS-1,d0
.fill:
	; a0 = input data (moves!)
	move.l	a3,d1
	add.l	(a0)+,d1
	move.l	d1,ymunp_stream_read_ptr(a1)		; ymunp_stream_read_ptr
	clr.l	ymunp_match_read_ptr(a1)		; ymunp_match_read_ptr
	move.l	a2,ymunp_cache_start_ptr(a1)		; ymunp_cache_start_ptr
	move.l	a2,ymunp_cache_write_ptr(a1)		; ymunp_cache_write_ptr
	lea	cache_size(a2),a2
	move.l	a2,ymunp_cache_end_ptr(a1)		; ymunp_cache_end_ptr
	move.w	#cache_size,ymunp_cache_size_w(a1)	; ymunp_cache_size_w
	move.w	#1,ymunp_size_count_w(a1)		; ymunp_size_count_w
	lea	ymunp_size(a1),a1			; next stream state
	dbf	d0,.fill
	rts

; a0 = input structure
player_update:
	lea	player_state,a0
	lea	output_buffer,a6
	moveq	#NUM_REGS-1,d1
	move.w	#ymunp_size,d2			; d2 = stream structure size
stream_update:
	; a0	= ymunp struct
	subq.w	#1,ymunp_size_count_w(a0)
	bne.s	stream_copy_one			; still in copying state

	; Set up next ymunp_match_read_ptr and ymunp_size_count_w here
	move.l	ymunp_stream_read_ptr(a0),a1	; a1 = packed data stream
	moveq	#0,d0
	move.b	(a1)+,d0
	; Match or reference?
	bclr	#7,d0
	bne.s	literals

	; Match code
	; a1 is the stream read ptr
	; d0 is the pre-read count value
	bsr.s	read_extended_number
	move.w	d0,ymunp_size_count_w(a0)

	; Now read offset
	moveq	#0,d0
	move.b	(a1)+,d0
	bsr.s	read_extended_number
	move.l	a1,ymunp_stream_read_ptr(a0)	; remember stream ptr now, before trashing a1

	; Apply offset backwards from where we are writing
	move.l	ymunp_cache_write_ptr(a0),a1
	sub.l	d0,a1
	cmp.l	ymunp_cache_start_ptr(a0),a1
	bge.s	no_loop2
	add.w	ymunp_cache_size_w(a0),a1
no_loop2:
	move.l	a1,ymunp_match_read_ptr(a0)
	bra.s	stream_copy_one
literals:
	; Literals code -- just a count
	; a1 is the stream read ptr
	; d0 is the pre-read count value
	bsr.s	read_extended_number
	move.w	d0,ymunp_size_count_w(a0)
	move.l	a1,ymunp_match_read_ptr(a0)	; use the current packed stream address
	add.l	d0,a1				; skip bytes in input stream
	move.l	a1,ymunp_stream_read_ptr(a0)
	; Falls through to do the copy

stream_copy_one:
	; Copy byte from either the cache or the literals in the stream
	movem.l	ymunp_match_read_ptr(a0),a1/a2/a3	; a1 = match read, a2 = cache write, a3 = loop addr
	move.b	(a1)+,d0			; d0 = output result
	move.b	d0,(a2)+			; add to cache

	; Handle either the read or write pointers hitting the end of the cache
	cmp.l	a3,a1				; loop?
	bne.s	noloop_cache_read
	sub.w	ymunp_cache_size_w(a0),a1	; move back in cache
noloop_cache_read:
	cmp.l	a3,a2				; loop?
	bne.s	noloop_cache_write
	sub.w	ymunp_cache_size_w(a0),a2	; move back in cache
noloop_cache_write:
	movem.l	a1/a2,ymunp_match_read_ptr(a0)

	; d0 is "output" here
	move.b	d0,(a6)+			; write to output buffer
	add.w	d2,a0				; next stream structure
	dbf	d1,stream_update
	bra.s	ym_write

read_extended_number:
	tst.b	d0
	bne.s	valid_count
	move.b	(a1)+,d0
	lsl.w	#8,d0
	move.b	(a1)+,d0
valid_count:
	rts

ym_write:
	lea	output_buffer(pc),a6
	lea	$ffff8800.w,a0
	lea	$ffff8802.w,a1

r	set	0
	rept	NUM_REGS
	ifne	r-13					; Buzzer envelope
		move.b	#r,(a0)
		move.b	(a6)+,(a1)
	else
		; Buzzer variant
		move.b	(a6)+,d0
		bmi.s	.skip_write
		move.b	#r,(a0)
		move.b	d0,(a1)
.skip_write:
	endif
r	set	r+1
	endr
	rts

		even
cache:		ds.b	cache_size*NUM_REGS
player_state:	ds.b	ymunp_size*NUM_REGS
output_buffer:	ds.b	NUM_REGS

		even
;player_data:	incbin	led2.ymp
;player_data:	incbin	sanxion.ymp
player_data:	incbin	motus.ymp
