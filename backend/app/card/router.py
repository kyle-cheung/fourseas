from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy.orm import Session
from ..database.connection import get_db
from .schemas import CreditCardCreate, CreditCardResponse, CreditCardUpdate
from .service import CreditCardService
from ..auth.jwt_auth import verify_token
from ..database.schemas import UserResponse
from uuid import UUID

router = APIRouter()

@router.post("/credit-cards", response_model=CreditCardResponse)
def create_credit_card(
    credit_card: CreditCardCreate,
    db: Session = Depends(get_db),
    current_user: UserResponse = Depends(verify_token)
):
    if credit_card.user_id != current_user.id:
        raise HTTPException(status_code=403, detail="Not authorized to create credit card for this user")
    return CreditCardService.create_credit_card(db, credit_card)

@router.get("/credit-cards/{credit_card_id}", response_model=CreditCardResponse)
def read_credit_card(
    credit_card_id: UUID,
    db: Session = Depends(get_db),
    current_user: UserResponse = Depends(verify_token)
):
    credit_card = CreditCardService.get_credit_card(db, credit_card_id)
    if credit_card.user_id != current_user.id:
        raise HTTPException(status_code=403, detail="Not authorized to access this credit card")
    return credit_card

@router.get("/users/me/credit-cards", response_model=list[CreditCardResponse])
def read_user_credit_cards(
    db: Session = Depends(get_db),
    current_user: UserResponse = Depends(verify_token)
):
    return CreditCardService.get_user_credit_cards(db, current_user.id)

@router.put("/credit-cards/{credit_card_id}", response_model=CreditCardResponse)
def update_credit_card(
    credit_card_id: UUID,
    credit_card_update: CreditCardUpdate,
    db: Session = Depends(get_db),
    current_user: UserResponse = Depends(verify_token)
):
    credit_card = CreditCardService.get_credit_card(db, credit_card_id)
    if credit_card.user_id != current_user.id:
        raise HTTPException(status_code=403, detail="Not authorized to update this credit card")
    return CreditCardService.update_credit_card(db, credit_card_id, credit_card_update)

@router.delete("/credit-cards/{credit_card_id}", status_code=204)
def delete_credit_card(
    credit_card_id: UUID,
    db: Session = Depends(get_db),
    current_user: UserResponse = Depends(verify_token)
):
    credit_card = CreditCardService.get_credit_card(db, credit_card_id)
    if credit_card.user_id != current_user.id:
        raise HTTPException(status_code=403, detail="Not authorized to delete this credit card")
    CreditCardService.delete_credit_card(db, credit_card_id)
    return {"detail": "Credit card deleted successfully"}