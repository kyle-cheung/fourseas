from sqlalchemy.orm import Session
from .models import CreditCard
from .schemas import CreditCardCreate, CreditCardUpdate
from fastapi import HTTPException
from uuid import UUID

class CreditCardService:
    @staticmethod
    def create_credit_card(db: Session, credit_card: CreditCardCreate) -> CreditCard:
        db_credit_card = CreditCard(**credit_card.model_dump())
        db.add(db_credit_card)
        db.commit()
        db.refresh(db_credit_card)
        return db_credit_card

    @staticmethod
    def get_credit_card(db: Session, credit_card_id: UUID) -> CreditCard:
        credit_card = db.query(CreditCard).filter(CreditCard.id == credit_card_id).first()
        if credit_card is None:
            raise HTTPException(status_code=404, detail="Credit card not found")
        return credit_card

    @staticmethod
    def get_user_credit_cards(db: Session, user_id: UUID) -> list[CreditCard]:
        return db.query(CreditCard).filter(CreditCard.user_id == user_id).all()

    @staticmethod
    def update_credit_card(db: Session, credit_card_id: UUID, credit_card_update: CreditCardUpdate) -> CreditCard:
        db_credit_card = CreditCardService.get_credit_card(db, credit_card_id)
        update_data = credit_card_update.model_dump(exclude_unset=True)
        for key, value in update_data.items():
            setattr(db_credit_card, key, value)
        db.commit()
        db.refresh(db_credit_card)
        return db_credit_card

    @staticmethod
    def delete_credit_card(db: Session, credit_card_id: UUID) -> None:
        db_credit_card = CreditCardService.get_credit_card(db, credit_card_id)
        db.delete(db_credit_card)
        db.commit()